// Package handlertest lets tests run scripted handler processes: the test
// binary re-executes itself (helper-process pattern) and, instead of running
// tests, acts as a handler speaking the NDJSON protocol of docs/design.md
// section 8.2.
//
// A test package opts in with
//
//	func TestMain(m *testing.M) { handlertest.Main(m) }
//
// and starts handlers with Command(mode).
package handlertest

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// helperArg marks a re-executed test binary as a scripted handler. The mode
// travels in argv rather than the environment, so tests stay parallel-safe.
const helperArg = "-tello.handlertest.mode"

// Modes of the scripted handler.
const (
	// Answerer replies to every user.turn with an answer whose text lists
	// the frame types received so far and the cli.start fields:
	// "seen=<t1>,<t2>,… to=<to> prompt=<prompt> index=<index|-> metadata=<json>".
	// The messageId is "m-<turnIndex>" and requestId "r-<turnIndex>". It
	// exits 0 at stdin EOF.
	Answerer = "answerer"
	// Echo answers every received line with text "got <type>" and exits 0
	// at stdin EOF.
	Echo = "echo"
	// Noise writes a blank line, malformed JSON, a non-object line, an
	// unknown command, then sendDtmf "1#" (messageId m1) and cancel, and
	// exits 0 at stdin EOF.
	Noise = "noise"
	// Exit3 writes "boom" to stderr and exits 3 without reading stdin.
	Exit3 = "exit3"
	// ExitOnCreated exits 4 as soon as it receives call.created.
	ExitOnCreated = "exitOnCreated"
	// Hang ignores stdin EOF and never exits on its own.
	Hang = "hang"
	// Groups answers its first input line with text "self=<pgid>
	// parent=<pgid of its parent>" (Unix; "unsupported" elsewhere) and
	// exits 0 at stdin EOF.
	Groups = "groups"
	// Env answers its first input line with text
	// "TELLO_API_KEY=<set|unset> TELLO_TEST_PASSTHROUGH=<value>" and exits
	// 0 at stdin EOF.
	Env = "env"
	// CancelOnCreated prints cancel when it receives call.created and
	// exits 0 right away.
	CancelOnCreated = "cancelOnCreated"
)

// Main runs m, or the scripted handler when the binary was re-executed by
// Command.
func Main(m *testing.M) {
	if len(os.Args) == 3 && os.Args[1] == helperArg {
		os.Exit(run(os.Args[2]))
	}
	// Under -race a child sleeps 1s at clean exit by default; handlers are
	// children of the test binary, so turn that off for them.
	os.Setenv("GORACE", strings.TrimSpace(os.Getenv("GORACE")+" atexit_sleep_ms=0"))
	os.Exit(m.Run())
}

// SyncBuffer is an io.Writer safe for concurrent writers. Use it as App.Stderr:
// a handler's stderr is copied there by os/exec on its own goroutine.
type SyncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *SyncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *SyncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Command is the argv that starts the scripted handler mode.
func Command(t testing.TB, mode string) []string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("handlertest: %v", err)
	}
	return []string{exe, helperArg, mode}
}

func run(mode string) int {
	out := bufio.NewWriter(os.Stdout)
	emit := func(line string) {
		out.WriteString(line + "\n")
		out.Flush()
	}
	command := func(event string, data map[string]any) {
		b, _ := json.Marshal(map[string]any{"event": event, "data": data})
		emit(string(b))
	}

	switch mode {
	case Exit3:
		fmt.Fprintln(os.Stderr, "boom")
		return 3
	case Hang:
		for {
			time.Sleep(time.Hour)
		}
	case Noise:
		emit("")
		emit("{not json")
		emit(`"just a string"`)
		emit(`{"event":"hangup","data":{}}`)
		command("sendDtmf", map[string]any{"digits": "1#", "messageId": "m1"})
		command("cancel", map[string]any{})
	}

	var seen []string
	var start map[string]any
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for in.Scan() {
		var frame map[string]any
		if err := json.Unmarshal(in.Bytes(), &frame); err != nil {
			fmt.Fprintf(os.Stderr, "handlertest: bad input line %q\n", in.Text())
			return 1
		}
		typ, _ := frame["type"].(string)
		seen = append(seen, typ)
		if typ == "cli.start" {
			start = frame
		}
		switch mode {
		case Echo:
			command("answer", map[string]any{"text": "got " + typ})
		case Groups:
			if len(seen) == 1 {
				command("answer", map[string]any{"text": processGroups()})
			}
		case Env:
			if len(seen) == 1 {
				_, hasKey := os.LookupEnv("TELLO_API_KEY")
				state := "unset"
				if hasKey {
					state = "set"
				}
				command("answer", map[string]any{"text": "TELLO_API_KEY=" + state + " TELLO_TEST_PASSTHROUGH=" + os.Getenv("TELLO_TEST_PASSTHROUGH")})
			}
		case CancelOnCreated:
			if typ == "call.created" {
				command("cancel", map[string]any{})
				return 0
			}
		case ExitOnCreated:
			if typ == "call.created" {
				return 4
			}
		case Answerer:
			if typ == "user.turn" {
				turn := fmt.Sprint(frame["turnIndex"])
				command("answer", map[string]any{
					"text":      describe(seen, start),
					"messageId": "m-" + turn,
					"requestId": "r-" + turn,
				})
			}
		}
	}
	return 0
}

func describe(seen []string, start map[string]any) string {
	index := "-"
	if v, ok := start["index"]; ok {
		index = fmt.Sprint(v)
	}
	metadata, _ := json.Marshal(start["metadata"])
	return fmt.Sprintf("seen=%s to=%v prompt=%v index=%s metadata=%s",
		strings.Join(seen, ","), start["to"], start["prompt"], index, metadata)
}
