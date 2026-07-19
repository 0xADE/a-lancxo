import unittest
import os
import time
import socket
import subprocess
import shutil
import tempfile
import stat

# Protocol constants
HEADER_SIZE = 5  # "TXT01"
HEADER_MAGIC = b"TXT01"


class TestAdeCtld(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        # Create temporary directory structure
        cls.test_dir = tempfile.mkdtemp(prefix="ade-test-")
        cls.home_dir = os.path.join(cls.test_dir, "home")
        cls.bin_dir = os.path.join(cls.test_dir, "bin")
        cls.socket_path = os.path.join(cls.test_dir, "ade.sock")

        os.makedirs(cls.home_dir)
        os.makedirs(cls.bin_dir)

        # Setup XDG paths
        cls.apps_dir = os.path.join(cls.home_dir, ".local", "share", "applications")
        os.makedirs(cls.apps_dir)

        # Create dummy executable
        cls.dummy_exec_path = os.path.join(cls.bin_dir, "dummy-app")
        with open(cls.dummy_exec_path, "w") as f:
            f.write("#!/bin/sh\necho 'Running dummy app'\n")
        os.chmod(cls.dummy_exec_path, stat.S_IRWXU)

        # Create dummy desktop file
        cls.desktop_file_path = os.path.join(cls.apps_dir, "dummy-app.desktop")
        with open(cls.desktop_file_path, "w") as f:
            f.write(
                """[Desktop Entry]
Type=Application
Name=Dummy App
Name[ru]=Тестовое Приложение
Exec=dummy-app
Terminal=false
Categories=Utility;Test;
"""
            )

        # Prepare environment
        cls.env = os.environ.copy()
        cls.env["HOME"] = cls.home_dir
        cls.env["PATH"] = cls.bin_dir + ":" + cls.env.get("PATH", "")
        cls.env["ADE_INDEXD_SOCK"] = cls.socket_path
        # Ensure we use a fast config
        cls.env["ADE_INDEXD_WORKERS"] = "1"

        # Build the server first (assuming 'make build' was run or binary exists)
        server_bin = os.path.abspath(
            os.path.join(os.path.dirname(__file__), "../build/a-lancxo")
        )
        if not os.path.exists(server_bin):
            raise RuntimeError(
                f"Server binary not found at {server_bin}. Run 'make build' first."
            )

        # Start server
        cls.server_proc = subprocess.Popen(
            [server_bin], env=cls.env, stdout=subprocess.PIPE, stderr=subprocess.PIPE
        )

        # Wait for socket to appear
        start_time = time.time()
        while time.time() - start_time < 5:
            if os.path.exists(cls.socket_path):
                break
            time.sleep(0.1)
        else:
            cls.server_proc.kill()
            raise RuntimeError("Server failed to start or create socket within timeout")

        # Give it a moment to initialize index
        time.sleep(2)

    @classmethod
    def tearDownClass(cls):
        if cls.server_proc:
            cls.server_proc.terminate()
            cls.server_proc.wait()
        shutil.rmtree(cls.test_dir)

    def connect(self):
        s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        s.connect(self.socket_path)
        s.sendall(HEADER_MAGIC)
        return s

    def send_command(self, s, cmd_str):
        s.sendall(cmd_str.encode("utf-8"))

    def read_response(self, s):
        # Read header
        header = s.recv(HEADER_SIZE)
        if header != HEADER_MAGIC:
            raise ValueError(f"Invalid header: {header}")

        # Read body until \n\n
        buffer = b""
        while True:
            chunk = s.recv(4096)
            if not chunk:
                break
            buffer += chunk
            if b"\n\n" in buffer:
                break

        return buffer.decode("utf-8")

    def parse_response(self, resp):
        parts = resp.split("\n\n", 1)
        headers_raw = parts[0]
        body = parts[1] if len(parts) > 1 else ""

        headers = {}
        for line in headers_raw.split("\n"):
            if ": " in line:
                key, val = line.split(": ", 1)
                headers[key] = val

        return headers, body

    def test_01_list_basic(self):
        s = self.connect()
        try:
            self.send_command(s, "list\n")
            resp = self.read_response(s)
            headers, body = self.parse_response(resp)

            self.assertIn("len", headers, f"Headers missing len: {headers}")
            # We expect some apps (system + dummy)
            self.assertTrue(int(headers["len"]) > 0)
        finally:
            s.close()

    def test_02_filter_name(self):
        s = self.connect()
        try:
            # Filter by "Dummy"
            self.send_command(s, '"Dummy\nfilter-name\n')
            resp = self.read_response(s)
            headers, _ = self.parse_response(resp)
            self.assertEqual(
                headers.get("cmd"), "filter-name", f"Unexpected response: {resp}"
            )
            self.assertEqual(headers.get("status"), "0")

            # List results
            self.send_command(s, "list\n")
            resp = self.read_response(s)
            headers, body = self.parse_response(resp)
            self.assertIn("Dummy App", body, "Dummy App not found after filtering")

            # Filter by something non-existent
            self.send_command(s, '"NonExistentThing\nfilter-name\n')
            self.read_response(s)  # consume response

            self.send_command(s, "list\n")
            resp = self.read_response(s)
            headers, body = self.parse_response(resp)

            # Check if len is 0 or if body is empty
            # Note: len header might show total matches?
            # Spec: "Return list of application names ... according to current filter set"
            # Returns: len: <total_count>
            self.assertEqual(
                int(headers["len"]), 0, f"Expected 0 results, got {headers.get('len')}"
            )

        finally:
            s.close()

    def test_03_reset_filters(self):
        s = self.connect()
        try:
            self.send_command(s, "0filters\n")
            resp = self.read_response(s)
            headers, _ = self.parse_response(resp)
            self.assertEqual(
                headers.get("cmd"), "0filters", f"Unexpected response: {resp}"
            )

            self.send_command(s, "list\n")
            resp = self.read_response(s)
            headers, body = self.parse_response(resp)
            self.assertTrue(int(headers["len"]) > 0)
        finally:
            s.close()

    def test_04_lang(self):
        s = self.connect()
        try:
            # Reset filters first
            self.send_command(s, "0filters\n")
            self.read_response(s)

            # Filter for Dummy to ensure it appears in list
            self.send_command(s, '"Dummy\n+filter-name\n')
            self.read_response(s)

            # Set lang to ru
            self.send_command(s, '"ru\nlang\n')
            resp = self.read_response(s)
            headers, _ = self.parse_response(resp)
            self.assertEqual(headers.get("lang"), "ru", f"Unexpected response: {resp}")

            # List and check for localized name
            self.send_command(s, "list\n")
            resp = self.read_response(s)
            headers, body = self.parse_response(resp)

            # "Тестовое Приложение" is the RU name for Dummy App
            self.assertIn(
                "Тестовое Приложение",
                body,
                f"Localized name not found in body:\n{body}",
            )

        finally:
            s.close()

    def test_05_filter_cat(self):
        s = self.connect()
        try:
            self.send_command(s, "0filters\n")
            self.read_response(s)

            # Reset lang to en to avoid localization confusion
            self.send_command(s, '"en\nlang\n')
            self.read_response(s)

            # Filter by category "Utility"
            self.send_command(s, '"Utility\n+filter-cat\n')
            self.read_response(s)

            # Also filter by name "Dummy" to ensure we find OUR Utility app, not others
            self.send_command(s, '"Dummy\n+filter-name\n')
            self.read_response(s)

            self.send_command(s, "list\n")
            resp = self.read_response(s)
            headers, body = self.parse_response(resp)
            self.assertIn("Dummy App", body, f"Dummy App not found in body:\n{body}")

        finally:
            s.close()

    def test_06_run_fail(self):
        s = self.connect()
        try:
            # Try to run non-existent ID
            self.send_command(s, "999999\nrun\n")
            resp = self.read_response(s)
            headers, _ = self.parse_response(resp)

            self.assertIn(
                "error-cmd", headers, f"Expected error-cmd in headers: {headers}"
            )
            self.assertEqual(headers["error-cmd"], "run")
        finally:
            s.close()


if __name__ == "__main__":
    unittest.main()
