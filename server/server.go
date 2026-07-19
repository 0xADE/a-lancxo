package server

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"

	"github.com/0xADE/a-lancxo/internal/config"
	"github.com/0xADE/a-lancxo/internal/indexer"
	"github.com/0xADE/a-lancxo/internal/runindex"
	"github.com/0xADE/a-lancxo/parser"
)

const (
	andOp = "and"
	orOp  = "or"
	notOp = "not"
)

// Server handles Unix socket connections and command execution
type Server struct {
	listener net.Listener
	indexer  *indexer.Indexer
	runIndex *runindex.RunIndex
	running  bool
	mu       sync.RWMutex
	filters  *Filters
	lang     string
}

// Filters stores current filter settings
type Filters struct {
	mu          sync.RWMutex
	nameFilters []FilterExpr
	catFilters  []FilterExpr
	pathFilters []FilterExpr
}

// FilterExpr represents a filter expression
type FilterExpr struct {
	Values []string
	Op     string // orOp, andOp, notOp
}

// NewServer creates a new server instance
func NewServer(idx *indexer.Indexer) (*Server, error) {
	cfg := config.Get()
	socketPath := cfg.UnixSocket()

	// Create directory if needed
	socketDir := filepath.Dir(socketPath)
	if err := os.MkdirAll(socketDir, 0750); err != nil {
		return nil, err
	}

	// Remove existing socket if it exists
	os.Remove(socketPath)

	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", socketPath)
	if err != nil {
		return nil, err
	}

	// Initialize run index
	runIdx, err := runindex.NewRunIndex()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize run index: %w", err)
	}

	return &Server{
		listener: listener,
		indexer:  idx,
		runIndex: runIdx,
		filters:  &Filters{},
		lang:     "en",
	}, nil
}

// Start starts the server
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	s.running = true
	s.mu.Unlock()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		conn, err := s.listener.Accept()
		if err != nil {
			s.mu.RLock()
			running := s.running
			s.mu.RUnlock()
			if !running {
				return nil
			}
			continue
		}

		go s.handleConnection(conn)
	}
}

// Stop stops the server
func (s *Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = false
	return s.listener.Close()
}

func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()

	log.Printf("[DEBUG] New connection accepted")

	p, err := parser.NewParser(conn)
	if err != nil {
		log.Printf("[ERROR] Failed to create parser: %v", err)
		s.writeError(conn, "parser", "invalid header", err.Error())
		return
	}

	for {
		cmd, err := p.ParseCommand()
		if err == io.EOF {
			log.Printf("[DEBUG] Connection closed by client")
			break
		}
		if err != nil {
			log.Printf("[ERROR] Parse error: %v", err)
			s.writeError(conn, "parser", "parse error", err.Error())
			continue
		}

		log.Printf("[DEBUG] Executing command: %s with %d args", cmd.Name, len(cmd.Args))
		s.executeCommand(conn, cmd)
	}
}

func (s *Server) executeCommand(conn net.Conn, cmd *parser.Command) {
	switch cmd.Name {
	case "filter-name":
		s.handleFilterNameReplace(conn, cmd)
	case "+filter-name":
		s.handleAddFilterName(conn, cmd)
	case "+filter-cat":
		s.handleFilterCat(conn, cmd)
	case "+filter-path":
		s.handleFilterPath(conn, cmd)
	case "0filters":
		s.handleResetFilters(conn)
	case "list":
		s.handleList(conn)
	case "list-next":
		s.handleListNext(conn, cmd)
	case "run":
		s.handleRun(conn, cmd)
	case "lang":
		s.handleLang(conn, cmd)
	case "reindex":
		s.handleReindex(conn, cmd)
	default:
		s.writeError(conn, cmd.Name, "unknown command", "Command not recognized")
	}
}

func (s *Server) handleFilterNameReplace(conn net.Conn, cmd *parser.Command) {
	log.Printf("[DEBUG] Handling filter-name command")
	s.filters.mu.Lock()
	defer s.filters.mu.Unlock()

	expr := FilterExpr{Values: []string{}, Op: andOp}
	for _, arg := range cmd.Args {
		switch arg.Type {
		case parser.TypeString:
			expr.Values = append(expr.Values, arg.Str)
		case parser.TypeBool:
			// Check the original string to distinguish between and/not
			if arg.Str == notOp {
				expr.Op = notOp
			} else if arg.Str == orOp || arg.Bool {
				expr.Op = orOp
			} else {
				expr.Op = andOp
			}
		}
	}

	if len(expr.Values) > 0 {
		s.filters.nameFilters = []FilterExpr{expr}
		log.Printf("[DEBUG] Replaced name filters with: %v (op: %s)", expr.Values, expr.Op)
	} else {
		s.filters.nameFilters = []FilterExpr{}
		log.Printf("[DEBUG] Cleared name filters")
	}

	// Send success response (returns filter-name as per spec)
	attrs := "cmd: filter-name\nstatus: 0\n\n\n"
	s.writeResponse(conn, attrs)
}

func (s *Server) handleAddFilterName(conn net.Conn, cmd *parser.Command) {
	log.Printf("[DEBUG] Handling +filter-name command")
	s.filters.mu.Lock()
	defer s.filters.mu.Unlock()

	expr := FilterExpr{Values: []string{}, Op: orOp}
	for _, arg := range cmd.Args {
		switch arg.Type {
		case parser.TypeString:
			expr.Values = append(expr.Values, arg.Str)
		case parser.TypeBool:
			// Check the original string to distinguish between and/not
			if arg.Str == notOp {
				expr.Op = notOp
			} else if arg.Str == "or" || arg.Bool {
				expr.Op = orOp
			} else {
				expr.Op = andOp
			}
		}
	}

	if len(expr.Values) > 0 {
		s.filters.nameFilters = append(s.filters.nameFilters, expr)
		log.Printf("[DEBUG] Added name filter: %v (op: %s)", expr.Values, expr.Op)
	}

	// Send success response
	attrs := "cmd: +filter-name\nstatus: 0\n\n\n"
	s.writeResponse(conn, attrs)
}

func (s *Server) handleFilterCat(conn net.Conn, cmd *parser.Command) {
	log.Printf("[DEBUG] Handling filter-cat command")
	s.filters.mu.Lock()
	defer s.filters.mu.Unlock()

	expr := FilterExpr{Values: []string{}, Op: andOp}
	for _, arg := range cmd.Args {
		switch arg.Type {
		case parser.TypeString:
			expr.Values = append(expr.Values, arg.Str)
		case parser.TypeBool:
			if arg.Bool {
				expr.Op = orOp
			} else {
				expr.Op = andOp
			}
		}
	}

	if len(expr.Values) > 0 {
		s.filters.catFilters = append(s.filters.catFilters, expr)
		log.Printf("[DEBUG] Added cat filter: %v (op: %s)", expr.Values, expr.Op)
	}

	// Send success response
	attrs := "cmd: +filter-cat\nstatus: 0\n\n\n"
	s.writeResponse(conn, attrs)
}

func (s *Server) handleFilterPath(conn net.Conn, cmd *parser.Command) {
	log.Printf("[DEBUG] Handling filter-path command")
	s.filters.mu.Lock()
	defer s.filters.mu.Unlock()

	expr := FilterExpr{Values: []string{}, Op: orOp}
	for _, arg := range cmd.Args {
		switch arg.Type {
		case parser.TypeString:
			expr.Values = append(expr.Values, arg.Str)
		case parser.TypeBool:
			if arg.Bool {
				expr.Op = orOp
			} else {
				expr.Op = andOp
			}
		}
	}

	if len(expr.Values) > 0 {
		s.filters.pathFilters = append(s.filters.pathFilters, expr)
		log.Printf("[DEBUG] Added path filter: %v (op: %s)", expr.Values, expr.Op)
	}

	// Send success response
	attrs := "cmd: +filter-path\nstatus: 0\n\n\n"
	s.writeResponse(conn, attrs)
}

func (s *Server) handleResetFilters(conn net.Conn) {
	log.Printf("[DEBUG] Resetting all filters")
	s.filters.mu.Lock()
	defer s.filters.mu.Unlock()
	s.filters.nameFilters = []FilterExpr{}
	s.filters.catFilters = []FilterExpr{}
	s.filters.pathFilters = []FilterExpr{}

	// Send success response
	attrs := "cmd: 0filters\nstatus: 0\n\n\n"
	s.writeResponse(conn, attrs)
}

func (s *Server) handleList(conn net.Conn) {
	log.Printf("[DEBUG] Handling list command")

	idx := s.indexer.GetIndex()
	allEntries := idx.GetAll()

	s.filters.mu.RLock()
	filtered := s.filterEntries(allEntries)
	s.filters.mu.RUnlock()

	// Sort by run frequency (most frequent first)
	s.sortByRunFrequency(filtered)

	log.Printf("[DEBUG] Found %d entries after filtering (total: %d)", len(filtered), len(allEntries))

	cfg := config.Get()
	limit := cfg.ListLimit()
	fullLen := len(filtered)

	attrs := strings.Builder{}
	attrs.WriteString(fmt.Sprintf("len: %d\n", fullLen))

	// Apply limit if needed
	var entriesToShow []*indexer.Entry
	if len(filtered) > limit {
		entriesToShow = filtered[:limit]
		attrs.WriteString(fmt.Sprintf("limited: %d\n", limit))
		attrs.WriteString("offset: 0\n")
		attrs.WriteString(fmt.Sprintf("list-next: %d %d\n", limit, limit))
	} else {
		entriesToShow = filtered
	}

	attrs.WriteString("\nbody:\n")

	body := strings.Builder{}
	for _, entry := range entriesToShow {
		name := entry.Name
		if s.lang != "" && entry.Names != nil {
			if locName, ok := entry.Names[s.lang]; ok {
				name = locName
			}
		}
		body.WriteString(fmt.Sprintf("%d %s\n", entry.ID, name))
	}

	s.writeResponse(conn, attrs.String()+body.String()+"\n\n")
	log.Printf("[DEBUG] List response sent")
}

func (s *Server) handleListNext(conn net.Conn, cmd *parser.Command) {
	log.Printf("[DEBUG] Handling list-next command")

	if len(cmd.Args) == 0 || cmd.Args[0].Type != parser.TypeInt {
		log.Printf("[ERROR] list-next command missing offset parameter")
		s.writeError(conn, "list-next", "missing offset", "list-next command requires an offset parameter")
		return
	}

	offset := int(cmd.Args[0].Int)
	if offset < 0 {
		log.Printf("[ERROR] list-next command invalid offset: %d", offset)
		s.writeError(conn, "list-next", "invalid offset", "offset must be non-negative")
		return
	}

	cfg := config.Get()
	limitSize := cfg.ListLimit()

	// Check if limit_size is provided as second argument
	if len(cmd.Args) >= 2 && cmd.Args[1].Type == parser.TypeInt {
		if cmd.Args[1].Int > 0 {
			limitSize = int(cmd.Args[1].Int)
		}
	}

	idx := s.indexer.GetIndex()
	allEntries := idx.GetAll()

	s.filters.mu.RLock()
	filtered := s.filterEntries(allEntries)
	s.filters.mu.RUnlock()

	fullLen := len(filtered)

	if offset >= fullLen {
		log.Printf("[ERROR] list-next offset %d out of bounds (total: %d)", offset, fullLen)
		s.writeError(conn, "list-next", "offset out of bounds", fmt.Sprintf("offset %d exceeds total entries %d", offset, fullLen))
		return
	}

	end := offset + limitSize
	if end > fullLen {
		end = fullLen
	}

	entriesToShow := filtered[offset:end]

	attrs := strings.Builder{}
	attrs.WriteString(fmt.Sprintf("len: %d\n", fullLen))
	attrs.WriteString(fmt.Sprintf("limited: %d\n", limitSize))
	attrs.WriteString(fmt.Sprintf("offset: %d\n", offset))

	// If there are more entries, add list-next header
	if end < fullLen {
		attrs.WriteString(fmt.Sprintf("list-next: %d %d\n", end, limitSize))
	}

	attrs.WriteString("\nbody:\n")

	body := strings.Builder{}
	for _, entry := range entriesToShow {
		name := entry.Name
		if s.lang != "" && entry.Names != nil {
			if locName, ok := entry.Names[s.lang]; ok {
				name = locName
			}
		}
		body.WriteString(fmt.Sprintf("%d %s\n", entry.ID, name))
	}

	s.writeResponse(conn, attrs.String()+body.String()+"\n\n")
	log.Printf("[DEBUG] list-next response sent (offset: %d, limit: %d, shown: %d)", offset, limitSize, len(entriesToShow))
}

func (s *Server) handleRun(conn net.Conn, cmd *parser.Command) {
	log.Printf("[DEBUG] Handling run command")

	var id int64
	forceTerminal := false

	// Check for optional "opt: terminal" argument
	if len(cmd.Args) > 0 && cmd.Args[0].Type == parser.TypeString && cmd.Args[0].Str == "opt: terminal" {
		forceTerminal = true
		if len(cmd.Args) < 2 || cmd.Args[1].Type != parser.TypeInt {
			log.Printf("[ERROR] Run command missing id parameter after opt: terminal")
			s.writeError(conn, "run", "missing id", "run command requires an id parameter after opt: terminal")
			return
		}
		id = cmd.Args[1].Int
	} else if len(cmd.Args) == 0 || cmd.Args[0].Type != parser.TypeInt {
		log.Printf("[ERROR] Run command missing id parameter")
		s.writeError(conn, "run", "missing id", "run command requires an id parameter")
		return
	} else {
		id = cmd.Args[0].Int
	}

	log.Printf("[DEBUG] Running application with id: %d, forceTerminal: %v", id, forceTerminal)

	idx := s.indexer.GetIndex()
	entry, ok := idx.Get(id)
	if !ok {
		log.Printf("[ERROR] Index %d not found", id)
		s.writeError(conn, "run", "index not found", "Can't run application, requested index not found.")
		return
	}

	log.Printf("[DEBUG] Found entry: %s, exec: %s, terminal: %v", entry.Name, entry.Exec, entry.Terminal)

	// Execute the command
	var execCmd *exec.Cmd
	if forceTerminal || entry.Terminal {
		cfg := config.Get()
		term := cfg.Terminal()
		//nolint:gosec // G204: launcher intentionally runs desktop Exec in terminal
		execCmd = exec.CommandContext(context.Background(), term, "--hold", "-e", entry.Exec)
		log.Printf("[DEBUG] Executing in terminal: %s -e %s", term, entry.Exec)
	} else {
		//nolint:gosec // G204: launcher intentionally runs desktop Exec
		execCmd = exec.CommandContext(context.Background(), entry.Exec)
		log.Printf("[DEBUG] Executing: %v", entry.Exec)
	}

	// Detach the process from the parent session to prevent terminal blocking
	execCmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	err := execCmd.Start()
	if err != nil {
		log.Printf("[ERROR] Failed to start command: %v", err)
		s.writeError(conn, "run", "execution failed", err.Error())
		return
	}

	pid := execCmd.Process.Pid
	log.Printf("[DEBUG] Command started successfully with PID: %d", pid)

	// Update run frequency after successful execution
	if err := s.runIndex.Increment(entry.Path); err != nil {
		log.Printf("[WARN] Failed to update run frequency for %s: %v", entry.Path, err)
	}

	attrs := fmt.Sprintf("cmd: run\nidx: %d\nstatus: 0\npid: %d\n\n\n", id, pid)
	s.writeResponse(conn, attrs)
	log.Printf("[DEBUG] Run response sent")
}

func (s *Server) handleLang(conn net.Conn, cmd *parser.Command) {
	log.Printf("[DEBUG] Handling lang command")
	if len(cmd.Args) == 0 || cmd.Args[0].Type != parser.TypeString {
		log.Printf("[WARN] Lang command missing string parameter")
		s.writeError(conn, "lang", "missing parameter", "lang command requires a string parameter")
		return
	}
	s.lang = cmd.Args[0].Str
	log.Printf("[DEBUG] Language set to: %s", s.lang)

	// Send success response
	attrs := fmt.Sprintf("cmd: lang\nstatus: 0\nlang: %s\n\n\n", s.lang)
	s.writeResponse(conn, attrs)
}

func (s *Server) handleReindex(conn net.Conn, cmd *parser.Command) {
	log.Printf("[DEBUG] Handling reindex command")

	// Collect string arguments as paths
	paths := make([]string, 0, len(cmd.Args))
	for _, arg := range cmd.Args {
		if arg.Type != parser.TypeString {
			log.Printf("[ERROR] reindex command received non-string argument")
			s.writeError(conn, "reindex", "invalid argument", "reindex command accepts only string path arguments")
			return
		}
		paths = append(paths, arg.Str)
	}

	// Expand paths (handle ~ and convert to absolute)
	expandedPaths := make([]string, 0, len(paths))
	for _, path := range paths {
		expanded := s.expandPath(path)
		absPath, err := filepath.Abs(expanded)
		if err != nil {
			log.Printf("[WARN] Failed to resolve absolute path for %s: %v", path, err)
			// Use expanded path even if absolute resolution fails
			expandedPaths = append(expandedPaths, expanded)
		} else {
			expandedPaths = append(expandedPaths, absPath)
		}
	}

	log.Printf("[DEBUG] Reindexing paths: %v", expandedPaths)

	// Perform reindexing (blocking call)
	ctx := context.Background()
	count, err := s.indexer.Reindex(ctx, expandedPaths)
	if err != nil {
		log.Printf("[ERROR] Reindex failed: %v", err)
		s.writeError(conn, "reindex", "indexing failed", err.Error())
		return
	}

	log.Printf("[DEBUG] Reindex completed, indexed %d entries", count)

	// Send success response
	attrs := fmt.Sprintf("cmd: reindex\nstatus: 0\nindexed: %d\n\n\n", count)
	s.writeResponse(conn, attrs)
}

func (s *Server) expandPath(path string) string {
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return strings.Replace(path, "~", home, 1)
	}
	return path
}

func (s *Server) filterEntries(entries []*indexer.Entry) []*indexer.Entry {
	var result []*indexer.Entry

	for _, entry := range entries {
		if s.matchesFilters(entry) {
			result = append(result, entry)
		}
	}

	return result
}

func (s *Server) matchesFilters(entry *indexer.Entry) bool {
	// Check name filters
	if len(s.filters.nameFilters) > 0 {
		matched := false
		for _, filter := range s.filters.nameFilters {
			if s.matchesNameFilter(entry, filter) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	// Check category filters
	if len(s.filters.catFilters) > 0 {
		matched := false
		for _, filter := range s.filters.catFilters {
			if s.matchesCatFilter(entry, filter) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	// Check path filters
	if len(s.filters.pathFilters) > 0 {
		matched := false
		for _, filter := range s.filters.pathFilters {
			if s.matchesPathFilter(entry, filter) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	return true
}

func (s *Server) matchesNameFilter(entry *indexer.Entry, filter FilterExpr) bool {
	// Collect all searchable names (direct name + localized names)
	searchNames := []string{strings.ToLower(entry.Name)}
	for _, name := range entry.Names {
		searchNames = append(searchNames, strings.ToLower(name))
	}

	// Check matches for each value
	matches := make([]bool, len(filter.Values))
	for i, value := range filter.Values {
		valueLower := strings.ToLower(value)
		for _, searchName := range searchNames {
			if strings.Contains(searchName, valueLower) {
				matches[i] = true
				break
			}
		}
	}

	// Apply operation logic
	switch filter.Op {
	case orOp:
		// OR: return true if ANY value matches
		for _, match := range matches {
			if match {
				return true
			}
		}
		return false
	case andOp:
		// AND: return true if ALL values match
		for _, match := range matches {
			if !match {
				return false
			}
		}
		return len(matches) > 0
	case notOp:
		// NOT: return true if NONE of the values match
		for _, match := range matches {
			if match {
				return false
			}
		}
		return true
	default:
		// Default to OR behavior
		for _, match := range matches {
			if match {
				return true
			}
		}
		return false
	}
}

func (s *Server) matchesCatFilter(entry *indexer.Entry, filter FilterExpr) bool {
	for _, cat := range entry.Categories {
		for _, filterCat := range filter.Values {
			if strings.EqualFold(cat, filterCat) {
				return true
			}
		}
	}
	return false
}

func (s *Server) matchesPathFilter(entry *indexer.Entry, filter FilterExpr) bool {
	for _, filterPath := range filter.Values {
		if strings.Contains(entry.Path, filterPath) {
			return true
		}
	}
	return false
}

// writeResponse writes a response with TXT01 header
// Response string should already contain \n\n at the end to mark end of response
func (s *Server) writeResponse(conn net.Conn, response string) {
	log.Printf("[DEBUG] Writing response (length: %d bytes)", len(response))
	header := []byte("TXT01")
	n, err := conn.Write(header)
	if err != nil {
		log.Printf("[ERROR] Failed to write header: %v", err)
		return
	}
	if n != len(header) {
		log.Printf("[ERROR] Partial header write: %d/%d bytes", n, len(header))
		return
	}

	n, err = conn.Write([]byte(response))
	if err != nil {
		log.Printf("[ERROR] Failed to write response body: %v", err)
		return
	}
	log.Printf("[DEBUG] Response written successfully: %d bytes", n)
}

func (s *Server) writeError(conn net.Conn, cmd, errType, desc string) {
	log.Printf("[ERROR] Writing error response: cmd=%s, type=%s, desc=%s", cmd, errType, desc)
	errorMsg := fmt.Sprintf("error-cmd: %s\nerror: %s\ndesc: %s\n\n\n", cmd, errType, desc)
	s.writeResponse(conn, errorMsg)
}

// sortByRunFrequency sorts entries by run frequency in descending order (most frequent first)
func (s *Server) sortByRunFrequency(entries []*indexer.Entry) {
	// Collect all paths for batch frequency lookup
	paths := make([]string, len(entries))
	for i, entry := range entries {
		paths[i] = entry.Path
	}

	// Get frequencies for all paths in one call
	frequencies := s.runIndex.GetFrequencies(paths)

	// Sort entries by frequency (descending), then by ID (ascending) for stable sort
	sort.SliceStable(entries, func(i, j int) bool {
		freqI := frequencies[entries[i].Path]
		freqJ := frequencies[entries[j].Path]
		if freqI != freqJ {
			return freqI > freqJ // Higher frequency first
		}
		// If frequencies are equal, sort by ID for stable ordering
		return entries[i].ID < entries[j].ID
	})
}
