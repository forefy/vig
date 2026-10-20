package chain

import (
	"strconv"
	"strings"
)

// A transaction's log messages carry its call tree for free: every invoke
// with its depth, the instruction name when the program is Anchor, the
// units each call burned and where it failed. A logs subscription delivers
// them the moment the transaction finalizes, so most rows of the watch
// need no further read.

// LogFrame is one program invocation as the logs tell it.
type LogFrame struct {
	Program     string
	Depth       int    // 1 is a top-level instruction
	Seq         int    // position among every call of the transaction, in execution order
	Instruction string // the Anchor "Instruction: Name" log of this frame, snake case; empty otherwise
	Units       uint64 // consumed, when the runtime logged it
	Failed      bool
	Error       string // the failure line's reason, or the Anchor error code name
	ErrorCode   int    // custom program error code, when the reason carries one
	Parent      *LogFrame
	Children    []*LogFrame
	Truncated   bool
}

// LogTrace is a transaction's calls as a forest of frames.
type LogTrace struct {
	Top       []*LogFrame
	All       []*LogFrame // execution order
	Truncated bool
}

// ParseLogs builds the call tree from log messages.
func ParseLogs(logs []string) LogTrace {
	var t LogTrace
	var stack []*LogFrame
	seq := 0
	for _, line := range logs {
		switch {
		case strings.HasPrefix(line, "Program ") && strings.Contains(line, " invoke ["):
			rest := strings.TrimPrefix(line, "Program ")
			pid, tail, _ := strings.Cut(rest, " invoke [")
			depth, _ := strconv.Atoi(strings.TrimSuffix(tail, "]"))
			// A malformed depth still nests under whatever is open.
			for len(stack) > 0 && (depth <= 0 || stack[len(stack)-1].Depth >= depth) {
				stack = stack[:len(stack)-1]
			}
			seq++
			f := &LogFrame{Program: pid, Depth: depth, Seq: seq}
			if depth <= 0 {
				f.Depth = len(stack) + 1
			}
			if len(stack) > 0 {
				f.Parent = stack[len(stack)-1]
				f.Parent.Children = append(f.Parent.Children, f)
			} else {
				t.Top = append(t.Top, f)
			}
			t.All = append(t.All, f)
			stack = append(stack, f)
		case strings.HasPrefix(line, "Program log: "):
			if len(stack) == 0 {
				continue
			}
			f := stack[len(stack)-1]
			msg := strings.TrimPrefix(line, "Program log: ")
			switch {
			case strings.HasPrefix(msg, "Instruction: ") && f.Instruction == "":
				f.Instruction = snakeName(strings.TrimSpace(strings.TrimPrefix(msg, "Instruction: ")))
			case strings.HasPrefix(msg, "AnchorError"):
				if code := between(msg, "Error Code: ", "."); code != "" {
					f.Error = code
				}
				if n := between(msg, "Error Number: ", "."); n != "" {
					f.ErrorCode, _ = strconv.Atoi(n)
				}
			}
		case strings.HasPrefix(line, "Program ") && strings.Contains(line, " consumed "):
			rest := strings.TrimPrefix(line, "Program ")
			pid, tail, _ := strings.Cut(rest, " consumed ")
			digits, _, _ := strings.Cut(tail, " ")
			if f := open(stack, pid); f != nil {
				f.Units, _ = strconv.ParseUint(digits, 10, 64)
			}
		case strings.HasPrefix(line, "Program ") && strings.HasSuffix(line, " success"):
			pid := strings.TrimSuffix(strings.TrimPrefix(line, "Program "), " success")
			stack = closeFrame(stack, pid)
		case strings.HasPrefix(line, "Program ") && strings.Contains(line, " failed: "):
			rest := strings.TrimPrefix(line, "Program ")
			pid, reason, _ := strings.Cut(rest, " failed: ")
			if f := open(stack, pid); f != nil {
				f.Failed = true
				if f.Error == "" {
					f.Error = reason
				}
				if code, ok := customCode(reason); ok && f.ErrorCode == 0 {
					f.ErrorCode = code
				}
				// A failure unwinds every caller above it.
				for p := f.Parent; p != nil; p = p.Parent {
					p.Failed = true
				}
			}
			stack = closeFrame(stack, pid)
		case line == "Log truncated":
			t.Truncated = true
			for _, f := range stack {
				f.Truncated = true
			}
		}
	}
	return t
}

// open finds the innermost open frame of a program.
func open(stack []*LogFrame, pid string) *LogFrame {
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i].Program == pid {
			return stack[i]
		}
	}
	return nil
}

// closeFrame pops through the innermost open frame of a program.
func closeFrame(stack []*LogFrame, pid string) []*LogFrame {
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i].Program == pid {
			return stack[:i]
		}
	}
	return stack
}

// customCode reads "custom program error: 0x1771".
func customCode(reason string) (int, bool) {
	const tag = "custom program error: "
	i := strings.Index(reason, tag)
	if i < 0 {
		return 0, false
	}
	s := strings.TrimSpace(reason[i+len(tag):])
	if strings.HasPrefix(s, "0x") {
		n, err := strconv.ParseInt(s[2:], 16, 64)
		return int(n), err == nil
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

func between(s, from, to string) string {
	i := strings.Index(s, from)
	if i < 0 {
		return ""
	}
	s = s[i+len(from):]
	if j := strings.Index(s, to); j >= 0 {
		s = s[:j]
	}
	return strings.TrimSpace(s)
}

// snakeName turns Anchor's CamelCase instruction log into the snake name
// the IDL and the map use.
func snakeName(s string) string {
	out := make([]byte, 0, len(s)+4)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			if i > 0 && (s[i-1] < 'A' || s[i-1] > 'Z') || (i > 0 && i+1 < len(s) && s[i+1] >= 'a' && s[i+1] <= 'z') {
				out = append(out, '_')
			}
			out = append(out, c+32)
			continue
		}
		out = append(out, c)
	}
	return string(out)
}

// Callees lists the direct child programs of a frame, in order, once each.
func (f *LogFrame) Callees() []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range f.Children {
		if !seen[c.Program] {
			seen[c.Program] = true
			out = append(out, c.Program)
		}
	}
	return out
}

// Root is the top-level frame above f, or f itself.
func (f *LogFrame) Root() *LogFrame {
	for f.Parent != nil {
		f = f.Parent
	}
	return f
}
