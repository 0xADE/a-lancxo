// Deprecated version. Use a-lancxo instead.
package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/0xADE/a-lancxo/client/exe"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <command> [args...]\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Commands:\n")
		fmt.Fprintf(os.Stderr, "  list                     - List all applications\n")
		fmt.Fprintf(os.Stderr, "  list-next <offset> [limit] - Get next page of results\n")
		fmt.Fprintf(os.Stderr, "  filter-name <name>       - Filter by name\n")
		fmt.Fprintf(os.Stderr, "  filter-cat <cat>         - Filter by category\n")
		fmt.Fprintf(os.Stderr, "  reset-filters            - Reset all filters\n")
		fmt.Fprintf(os.Stderr, "  run <id>                 - Run application by ID\n")
		fmt.Fprintf(os.Stderr, "  lang <locale>            - Set language\n")
		fmt.Fprintf(os.Stderr, "  interactive              - Interactive mode\n")
		os.Exit(1)
	}

	// Create client
	client, err := exe.NewClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create client: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	cmd := os.Args[1]

	if cmd == "interactive" {
		runInteractive(client)
		return
	}

	// Execute command
	switch cmd {
	case "list":
		if err := client.SendCommand("list", nil); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to send command: %v\n", err)
			os.Exit(1)
		}
	case "list-next":
		if len(os.Args) < 3 {
			fmt.Fprintf(os.Stderr, "Usage: %s list-next <offset> [limit_size]\n", os.Args[0])
			os.Exit(1)
		}
		if err := client.SendCommand("list-next", os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to send command: %v\n", err)
			os.Exit(1)
		}
	case "filter-name":
		if len(os.Args) < 3 {
			fmt.Fprintf(os.Stderr, "Usage: %s filter-name <name>\n", os.Args[0])
			os.Exit(1)
		}
		if err := client.SendCommand("+filter-name", []string{os.Args[2]}); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to send command: %v\n", err)
			os.Exit(1)
		}
	case "filter-cat":
		if len(os.Args) < 3 {
			fmt.Fprintf(os.Stderr, "Usage: %s filter-cat <category>\n", os.Args[0])
			os.Exit(1)
		}
		if err := client.SendCommand("+filter-cat", []string{os.Args[2]}); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to send command: %v\n", err)
			os.Exit(1)
		}
	case "reset-filters":
		if err := client.SendCommand("0filters", nil); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to send command: %v\n", err)
			os.Exit(1)
		}
	case "run":
		if len(os.Args) < 3 {
			fmt.Fprintf(os.Stderr, "Usage: %s run <id>\n", os.Args[0])
			os.Exit(1)
		}
		if err := client.SendCommand("run", []string{os.Args[2]}); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to send command: %v\n", err)
			os.Exit(1)
		}
	case "reindex":
		if err := client.SendCommand("reindex", []string{os.Args[2]}); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to send command: %v\n", err)
			os.Exit(1)
		}
	case "lang":
		if len(os.Args) < 3 {
			fmt.Fprintf(os.Stderr, "Usage: %s lang <locale>\n", os.Args[0])
			os.Exit(1)
		}
		if err := client.SendCommand("lang", []string{os.Args[2]}); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to send command: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		os.Exit(1)
	}

	// Read and print response
	exe.ReadResponse(client.Conn())

	// Close connection and exit in non-interactive mode
	log.Printf("[DEBUG] Closing connection and exiting")
	os.Exit(0)
}

func runInteractive(client *exe.Client) {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("Interactive mode. Type commands or 'exit' to quit.")
	fmt.Print("> ")

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if line == "exit" || line == "quit" {
			break
		}

		if line == "" {
			fmt.Print("> ")
			continue
		}

		// Parse command
		parts := strings.Fields(line)
		if len(parts) == 0 {
			fmt.Print("> ")
			continue
		}

		cmd := parts[0]
		args := parts[1:]

		// Send command with type detection
		if err := client.SendCommand(cmd, args); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to send command: %v\n", err)
			fmt.Print("> ")
			continue
		}

		// Read response
		exe.ReadResponse(client.Conn())

		fmt.Print("> ")
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "Error reading input: %v\n", err)
	}
}
