package exe

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
)

// Application represents an application entry
type Application struct {
	ID   int64
	Name string
}

// Client handles connection to ade-exe-ctld server
type Client struct {
	conn   net.Conn
	reader *bufio.Reader
	mu     sync.Mutex
	socket string
}

const (
	protoVer   = "TXT01" // cmdlist protocol, text format, v01
	bodyMarker = "body:"
)

// NewClient creates a new client and connects to the server
func NewClient() (*Client, error) {
	socketPath, err := getSocketPath()
	if err != nil {
		return nil, fmt.Errorf("failed to get socket path: %w", err)
	}

	conn, err := (&net.Dialer{}).DialContext(context.Background(), "unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to socket %s: %w", socketPath, err)
	}

	// Send header
	if _, err := conn.Write([]byte(protoVer)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to send header: %w", err)
	}

	return &Client{
		conn:   conn,
		reader: bufio.NewReader(conn),
		socket: socketPath,
	}, nil
}

// Close closes the connection
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// FormatArgument formats an argument according to its type
func FormatArgument(arg any) string {
	switch v := arg.(type) {
	case int:
		return strconv.FormatInt(int64(v), 10)
	case int8:
		return strconv.FormatInt(int64(v), 10)
	case int16:
		return strconv.FormatInt(int64(v), 10)
	case int32:
		return strconv.FormatInt(int64(v), 10)
	case int64:
		return strconv.FormatInt(v, 10)
	case uint:
		return strconv.FormatUint(uint64(v), 10)
	case uint8:
		return strconv.FormatUint(uint64(v), 10)
	case uint16:
		return strconv.FormatUint(uint64(v), 10)
	case uint32:
		return strconv.FormatUint(uint64(v), 10)
	case uint64:
		return strconv.FormatUint(v, 10)
	case bool:
		if v {
			return "t"
		}
		return "f"
	case string:
		// If starts with ", it's a string (keep prefix)
		if strings.HasPrefix(v, `"`) {
			return v
		}

		// Check for boolean literals
		if v == "t" || v == "f" {
			return v
		}

		// Check for recognized keywords (boolean operators)
		keywords := []string{"or", "and", "not"}
		for _, kw := range keywords {
			if v == kw {
				return v
			}
		}

		// Default: treat as string (add prefix)
		return `"` + v
	default:
		// For other types, convert to string and add quotes
		return `"` + fmt.Sprintf("%v", arg) + `"`
	}
}

// SendCommand sends a command to the server
func (c *Client) SendCommand(cmdName string, args ...any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sendCommand(cmdName, args...)
}

// sendCommand is the internal version without locking
func (c *Client) sendCommand(cmdName string, args ...any) error {
	// Send arguments with type detection
	for _, arg := range args {
		formatted := FormatArgument(arg)
		if _, err := fmt.Fprintf(c.conn, "%s\n", formatted); err != nil {
			return fmt.Errorf("failed to send argument: %w", err)
		}
	}

	// Send command
	if _, err := fmt.Fprintf(c.conn, "%s\n", cmdName); err != nil {
		return fmt.Errorf("failed to send command: %w", err)
	}

	return nil
}

// Conn returns the underlying connection
func (c *Client) Conn() net.Conn {
	return c.conn
}

func ReadResponse(conn net.Conn) {
	reader := bufio.NewReader(conn)

	// Read header
	header := make([]byte, 5)
	_, err := io.ReadFull(reader, header)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read response header: %v\n", err)
		return
	}

	// Read attrs block and check for body: header
	attrs := strings.Builder{}
	body := strings.Builder{}
	seenBodyHeader := false

	for {
		line, err := reader.ReadString('\n')
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Read error: %v\n", err)
			break
		}

		// Check if this is the body: header
		if strings.TrimSpace(line) == bodyMarker {
			seenBodyHeader = true
			// Continue to read body content (don't add body: to attrs or body)
			continue
		}

		// Check for end of response marker (\n\n)
		if line == "\n" {
			if isEndOfResponse(reader) {
				// Response ends here (with or without body)
				break
			}

			// Check if this blank line is part of headers before body:
			if isBlankLineBeforeBodyHeader(reader) {
				// This blank line is part of headers, save it
				attrs.WriteString(line)
				continue
			}

			// Single \n in headers or body - save it
			if !seenBodyHeader {
				attrs.WriteString(line)
			} else {
				body.WriteString(line)
			}
			continue
		}

		if !seenBodyHeader {
			// Still reading headers
			attrs.WriteString(line)
		} else {
			// Reading body content
			body.WriteString(line)
		}
	}

	// Print response to stdout
	fmt.Print(attrs.String())
	if seenBodyHeader {
		fmt.Print(bodyMarker + "\n")
		fmt.Print(body.String())
	}
}

// isEndOfResponse checks if we've reached the end of response marker (\n\n)
func isEndOfResponse(reader *bufio.Reader) bool {
	peek, peekErr := reader.Peek(1)
	if peekErr == nil && len(peek) > 0 && peek[0] == '\n' {
		// Skip the second \n
		reader.ReadByte()
		return true
	}
	return false
}

// isBlankLineBeforeBodyHeader checks if this blank line is followed by a body: header
func isBlankLineBeforeBodyHeader(reader *bufio.Reader) bool {
	peekBytes, peekErr := reader.Peek(len(bodyMarker) + 1) // bodyMarker + "\n"
	if peekErr == nil && len(peekBytes) >= len(bodyMarker) {
		peekLine := string(peekBytes[:len(bodyMarker)])
		if peekLine == bodyMarker {
			return true
		}
	}
	return false
}

// ResetFilters resets all filters
func (c *Client) ResetFilters() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Send reset filters command
	if err := c.sendCommand("0filters"); err != nil {
		return fmt.Errorf("failed to send reset filters command: %w", err)
	}

	// Read response
	attrs, _, err := c.readResponse()
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	// Check for errors
	if errMsg, ok := attrs["error"]; ok {
		return fmt.Errorf("server error: %s", errMsg)
	}

	return nil
}

// SetFilterName sets a name filter
func (c *Client) SetFilterName(query string) error {
	if query == "" {
		return c.ResetFilters()
	}

	if err := c.SendCommand("filter-name", query); err != nil {
		return fmt.Errorf("failed to set name filter command: %w", err)
	}

	// Read response
	attrs, _, err := c.readResponse()
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	// Check for errors
	if errMsg, ok := attrs["error"]; ok {
		return fmt.Errorf("server error: %s", errMsg)
	}

	return nil
}

// List retrieves the list of applications matching current filters
func (c *Client) List() ([]Application, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Send list command
	if err := c.sendCommand("list"); err != nil {
		return nil, fmt.Errorf("failed to send list command: %w", err)
	}

	// Read response
	attrs, body, err := c.readResponse()
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Check for errors
	if errMsg, ok := attrs["error"]; ok {
		return nil, fmt.Errorf("server error: %s", errMsg)
	}

	// Parse body
	lines := strings.Split(strings.TrimSpace(body), "\n")
	apps := make([]Application, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		id, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			continue
		}
		name := strings.Join(parts[1:], " ")
		apps = append(apps, Application{
			ID:   id,
			Name: name,
		})
	}

	return apps, nil
}

// Run executes an application by ID
func (c *Client) Run(id int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Send run command with id
	if err := c.sendCommand("run", id); err != nil {
		return fmt.Errorf("failed to send run command: %w", err)
	}

	// Read response
	attrs, _, err := c.readResponse()
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	// Check for errors
	if errMsg, ok := attrs["error"]; ok {
		return fmt.Errorf("server error: %s", errMsg)
	}

	return nil
}

// RunInTerminal executes an application by ID in a terminal
func (c *Client) RunInTerminal(id int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Send opt: terminal, id, run
	if err := c.sendCommand("run", "opt: terminal", id); err != nil {
		return fmt.Errorf("failed to send run command: %w", err)
	}

	// Read response
	attrs, _, err := c.readResponse()
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	// Check for errors
	if errMsg, ok := attrs["error"]; ok {
		return fmt.Errorf("server error: %s", errMsg)
	}

	return nil
}

// readResponse is a private method that returns parsed response
func (c *Client) readResponse() (map[string]string, string, error) {
	reader := c.reader

	// Read header
	header := make([]byte, 5)
	_, err := io.ReadFull(reader, header)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read response header: %w", err)
	}

	// Read attrs block and check for body: header
	attrs := make(map[string]string)
	body := strings.Builder{}
	seenBodyHeader := false

	for {
		line, err := reader.ReadString('\n')
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, "", fmt.Errorf("read error: %w", err)
		}

		// Check if this is the body: header
		if strings.TrimSpace(line) == bodyMarker {
			seenBodyHeader = true
			// Continue to read body content (don't add body: to attrs or body)
			continue
		}

		// Check for end of response marker (\n\n)
		if line == "\n" {
			if isEndOfResponse(reader) {
				// Response ends here (with or without body)
				break
			}

			// Check if this blank line is part of headers before body:
			if isBlankLineBeforeBodyHeader(reader) {
				// This blank line is part of headers, save it
				continue
			}

			// Single \n in headers or body - save it
			if !seenBodyHeader {
				// In headers, blank lines are ignored
			} else {
				body.WriteString(line)
			}
			continue
		}

		if !seenBodyHeader {
			// Still reading headers
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				attrs[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
			}
		} else {
			// Reading body content
			body.WriteString(line)
		}
	}

	return attrs, body.String(), nil
}
