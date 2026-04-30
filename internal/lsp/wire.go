// Package lsp implements v0.3 IDE/LSP mode: a daemon (ae lsp) that hosts
// language servers, caches diagnostics in SQLite, and serves symbol/
// reference/definition queries over a Unix socket.
//
// Wire protocol (this file): newline-delimited shortform messages on the
// socket, with multi-line content blocks framed by `content`...`end`
// sentinels. Both daemon and CLI use this parser.
package lsp

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Request is one client-to-daemon message.
//
// Verb is one of: sym, def, ref, syms, notify, ping.
// Args carry the per-verb positional arguments.
// Content is set when the message includes a content block (notify changed).
type Request struct {
	Verb    string
	Args    []string
	Content []string
}

// Response is one daemon-to-client record. Multiple records terminated by
// an `end` sentinel form a complete reply.
type Response struct {
	Kind   string   // sym, def, ref, ok, error, status, notify_ack
	Fields []string // tab-positional fields, e.g. [kind, loc, name]
}

// Encode writes a Request as one or more lines to w.
func (r Request) Encode(w io.Writer) error {
	parts := append([]string{r.Verb}, r.Args...)
	if _, err := fmt.Fprintln(w, strings.Join(parts, " ")); err != nil {
		return err
	}
	if len(r.Content) > 0 {
		if _, err := fmt.Fprintln(w, "content"); err != nil {
			return err
		}
		for _, line := range r.Content {
			if _, err := fmt.Fprintln(w, line); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w, "end"); err != nil {
			return err
		}
	}
	return nil
}

// DecodeRequest reads one Request from r. Returns io.EOF when the connection
// is closed cleanly between requests.
func DecodeRequest(r *bufio.Reader) (*Request, error) {
	header, err := readLine(r)
	if err != nil {
		return nil, err
	}
	if header == "" {
		// Skip blank keepalive lines instead of mis-parsing.
		return DecodeRequest(r)
	}
	tokens := strings.Fields(header)
	if len(tokens) == 0 {
		return nil, errors.New("wire: empty request line")
	}
	req := &Request{Verb: tokens[0], Args: tokens[1:]}
	// Only "notify" carries a content block. For every other verb the
	// request is one line and we must NOT block reading more bytes.
	if req.Verb != "notify" {
		return req, nil
	}
	// Read the "content" sentinel line (the spec frames the body explicitly).
	mark, err := readLine(r)
	if err != nil {
		return nil, err
	}
	if mark != "content" {
		// The notify request had no body; treat as legal.
		return req, nil
	}
	for {
		line, err := readLine(r)
		if err != nil {
			return nil, err
		}
		if line == "end" {
			break
		}
		req.Content = append(req.Content, line)
	}
	return req, nil
}

// EncodeResponses writes the slice plus the terminating `end` sentinel.
func EncodeResponses(w io.Writer, rs []Response) error {
	for _, resp := range rs {
		fields := append([]string{resp.Kind}, resp.Fields...)
		if _, err := fmt.Fprintln(w, strings.Join(fields, "\t")); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w, "end")
	return err
}

// DecodeResponses reads response records until the `end` sentinel.
func DecodeResponses(r *bufio.Reader) ([]Response, error) {
	var out []Response
	for {
		line, err := readLine(r)
		if err != nil {
			return out, err
		}
		if line == "end" {
			return out, nil
		}
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		out = append(out, Response{Kind: fields[0], Fields: fields[1:]})
	}
}

// ErrorResponse returns the canonical lsp_unavailable error reply.
func ErrorResponse(reason string) []Response {
	return []Response{{Kind: "error", Fields: []string{"lsp_unavailable", reason}}}
}

// SymbolResponse formats one symbol record as a wire response.
func SymbolResponse(kind, loc, name string) Response {
	return Response{Kind: "sym", Fields: []string{kind, loc, name}}
}

// DefResponse formats a def record.
func DefResponse(loc, name, kind string) Response {
	return Response{Kind: "def", Fields: []string{loc, name, kind}}
}

// RefResponse formats a ref record.
func RefResponse(loc, usage, context string) Response {
	return Response{Kind: "ref", Fields: []string{loc, usage, context}}
}

// FormatLocation builds "path:line:col" given 1-indexed coordinates.
func FormatLocation(path string, line, col int) string {
	return path + ":" + strconv.Itoa(line) + ":" + strconv.Itoa(col)
}

// ParseLocation reverses FormatLocation. Returns path, line, col, error.
func ParseLocation(loc string) (string, int, int, error) {
	idx := strings.LastIndex(loc, ":")
	if idx < 0 {
		return "", 0, 0, fmt.Errorf("wire: bad location %q", loc)
	}
	col, err := strconv.Atoi(loc[idx+1:])
	if err != nil {
		return "", 0, 0, fmt.Errorf("wire: bad location col in %q", loc)
	}
	rest := loc[:idx]
	idx2 := strings.LastIndex(rest, ":")
	if idx2 < 0 {
		return "", 0, 0, fmt.Errorf("wire: bad location %q", loc)
	}
	line, err := strconv.Atoi(rest[idx2+1:])
	if err != nil {
		return "", 0, 0, fmt.Errorf("wire: bad location line in %q", loc)
	}
	return rest[:idx2], line, col, nil
}

func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
