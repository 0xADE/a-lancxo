package server

import (
	"bytes"
	"net"
	"time"

	"github.com/0xADE/a-lancxo/internal/indexer"
	"github.com/0xADE/a-lancxo/parser"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("handleReindex", func() {
	var (
		idx        *indexer.Indexer
		srv        *Server
		clientConn net.Conn
		serverConn net.Conn
		response   string
	)

	BeforeEach(func() {
		idx = indexer.NewIndexer()
		srv = &Server{indexer: idx}
	})

	AfterEach(func() {
		if clientConn != nil {
			clientConn.Close()
		}
		if serverConn != nil {
			serverConn.Close()
		}
	})

	Context("when handling reindex command with paths via TCP", func() {
		BeforeEach(func() {
			var err error
			clientConn, serverConn, err = createPipeConnection()
			Expect(err).NotTo(HaveOccurred())

			// Start parser goroutine
			go func() {
				defer serverConn.Close()
				p, err := parser.NewParser(serverConn)
				if err != nil {
					Fail("Failed to create parser: " + err.Error())
					return
				}

				cmd, err := p.ParseCommand()
				if err != nil {
					return
				}
				srv.executeCommand(serverConn, cmd)
			}()

			// Send reindex command with paths
			request := []byte("TXT01\"~/test/bin\n\"~/test/apps\nreindex\n")
			_, err = clientConn.Write(request)
			Expect(err).NotTo(HaveOccurred())

			// Read response
			response, err = readFullResponse(clientConn)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should contain command name", func() {
			Expect(response).To(ContainSubstring("cmd: reindex"))
		})

		It("should have successful status", func() {
			Expect(response).To(ContainSubstring("status: 0"))
		})

		It("should contain indexed count", func() {
			Expect(response).To(ContainSubstring("indexed:"))
		})
	})

	Context("when handling reindex command without arguments via TCP", func() {
		BeforeEach(func() {
			var err error
			clientConn, serverConn, err = createPipeConnection()
			Expect(err).NotTo(HaveOccurred())

			// Start parser goroutine
			go func() {
				defer serverConn.Close()
				p, err := parser.NewParser(serverConn)
				if err != nil {
					Fail("Failed to create parser: " + err.Error())
					return
				}

				cmd, err := p.ParseCommand()
				if err != nil {
					return
				}
				srv.executeCommand(serverConn, cmd)
			}()

			// Send reindex command without paths
			request := []byte("TXT01reindex\n")
			_, err = clientConn.Write(request)
			Expect(err).NotTo(HaveOccurred())

			// Read response
			response, err = readFullResponse(clientConn)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should contain command name", func() {
			Expect(response).To(ContainSubstring("cmd: reindex"))
		})

		It("should have successful status", func() {
			Expect(response).To(ContainSubstring("status: 0"))
		})
	})

	Context("when handling reindex command with invalid arguments", func() {
		BeforeEach(func() {
			var err error
			clientConn, serverConn, err = createPipeConnection()
			Expect(err).NotTo(HaveOccurred())

			// Create a command with invalid (non-string) argument
			cmd := &parser.Command{
				Name: "reindex",
				Args: []parser.Value{
					{Type: parser.TypeInt, Int: 123},
				},
			}

			// Handle command
			go func() {
				defer serverConn.Close()
				srv.executeCommand(serverConn, cmd)
			}()

			// Read response
			response, err = readFullResponse(clientConn)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should contain error command name", func() {
			Expect(response).To(ContainSubstring("error-cmd: reindex"))
		})

		It("should contain invalid argument error", func() {
			Expect(response).To(ContainSubstring("invalid argument"))
		})
	})

	Context("when calling handleReindex directly with paths", func() {
		var responseBuf bytes.Buffer
		var mockConnInstance *mockConn

		BeforeEach(func() {
			responseBuf.Reset()
			mockConnInstance = &mockConn{writeBuf: &responseBuf}

			cmd := createReindexCommand([]string{"/tmp/test1", "/tmp/test2"})
			srv.handleReindex(mockConnInstance, cmd)
			response = responseBuf.String()
		})

		It("should contain command name", func() {
			Expect(response).To(ContainSubstring("cmd: reindex"))
		})

		It("should have successful status", func() {
			Expect(response).To(ContainSubstring("status: 0"))
		})

		It("should contain indexed count", func() {
			Expect(response).To(ContainSubstring("indexed:"))
		})
	})
})

// Helper functions

// createPipeConnection creates a TCP pipe connection pair for testing
func createPipeConnection() (clientConn, serverConn net.Conn, err error) {
	clientConn, serverConn = net.Pipe()
	return clientConn, serverConn, nil
}

// readFullResponse reads the complete response from a connection
func readFullResponse(conn net.Conn) (string, error) {
	// Skip TXT01 header
	header := make([]byte, 5)
	n, err := conn.Read(header)
	if err != nil || n != 5 {
		return "", err
	}

	// Read response body
	response := make([]byte, 4096)
	n, err = conn.Read(response)
	if err != nil {
		return "", err
	}

	return string(response[:n]), nil
}

// createReindexCommand creates a test command for reindexing
func createReindexCommand(paths []string) *parser.Command {
	args := make([]parser.Value, len(paths))
	for i, path := range paths {
		args[i] = parser.Value{Type: parser.TypeString, Str: path}
	}
	return &parser.Command{
		Name: "reindex",
		Args: args,
	}
}

// mockConn implements net.Conn for testing
type mockConn struct {
	readBuf  *bytes.Buffer
	writeBuf *bytes.Buffer
}

func (m *mockConn) Read(b []byte) (n int, err error) {
	if m.readBuf == nil {
		return 0, nil
	}
	return m.readBuf.Read(b)
}

func (m *mockConn) Write(b []byte) (n int, err error) {
	if m.writeBuf == nil {
		return len(b), nil
	}
	return m.writeBuf.Write(b)
}

func (m *mockConn) Close() error {
	return nil
}

func (m *mockConn) LocalAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0}
}

func (m *mockConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0}
}

func (m *mockConn) SetDeadline(t time.Time) error {
	return nil
}

func (m *mockConn) SetReadDeadline(t time.Time) error {
	return nil
}

func (m *mockConn) SetWriteDeadline(t time.Time) error {
	return nil
}
