package toolenv

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/open-octo/octo-agent/internal/tools"
	"github.com/open-octo/octo-agent/pkg/octoagent"
)

type stubSender struct{}

func (stubSender) SendMessages(ctx context.Context, model, system string, messages []octoagent.Message, maxTokens int) (octoagent.Reply, error) {
	return octoagent.Reply{}, nil
}

func TestWireForSession_DoesNotTouchGate(t *testing.T) {
	a := octoagent.New(stubSender{}, "stub-model")

	// Use a custom gate with an observable sentinel so we can detect replacement.
	sentinel := &testGate{allow: true, reason: "original"}
	a.Gate = sentinel

	ctx := context.Background()
	_, _, cleanup := WireForSession(ctx, a, "session-gate-test")
	defer cleanup()

	if a.Gate != sentinel {
		t.Fatal("WireForSession modified Agent.Gate")
	}
}

type testGate struct {
	allow  bool
	reason string
}

func (g *testGate) Check(ctx context.Context, name string, input map[string]any) (bool, string) {
	return g.allow, g.reason
}

func TestWireForSession_IsolatesWorkingDir(t *testing.T) {
	ctx := context.Background()

	a1 := octoagent.New(stubSender{}, "model-a")
	a1.CWD = "/tmp/agent-a"
	ctx1, _, cleanup1 := WireForSession(ctx, a1, "session-a")
	defer cleanup1()

	a2 := octoagent.New(stubSender{}, "model-b")
	a2.CWD = "/tmp/agent-b"
	ctx2, _, cleanup2 := WireForSession(ctx, a2, "session-b")
	defer cleanup2()

	if got := tools.WorkingDir(ctx1); got != "/tmp/agent-a" {
		t.Fatalf("session-a working dir = %q, want /tmp/agent-a", got)
	}
	if got := tools.WorkingDir(ctx2); got != "/tmp/agent-b" {
		t.Fatalf("session-b working dir = %q, want /tmp/agent-b", got)
	}
}

func TestWireForSession_ConcurrentIsolation(t *testing.T) {
	ctx := context.Background()
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			a := octoagent.New(stubSender{}, "model")
			a.CWD = "/tmp/agent"
			_, executor, cleanup := WireForSession(ctx, a, "session")
			defer cleanup()

			if executor == nil {
				t.Error("executor is nil")
			}
		}(i)
	}

	wg.Wait()
}

func TestWireForSession_DoesNotImportConfigOrPermission(t *testing.T) {
	// WireForSession is documented as a zero-local-file-I/O path. We verify that
	// the core implementation file (internal/app/toolenv.go) does not directly
	// import or call the config/permission packages. Go package-level deps still
	// pull internal/app as a whole, but the core function itself stays clean.
	path := "../../../internal/app/toolenv.go"
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read toolenv.go: %v", err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.AllErrors)
	if err != nil {
		t.Fatalf("parse toolenv.go: %v", err)
	}

	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if path == "github.com/open-octo/octo-agent/internal/config" {
			t.Error("internal/app/toolenv.go imports internal/config")
		}
		if path == "github.com/open-octo/octo-agent/internal/permission" {
			t.Error("internal/app/toolenv.go imports internal/permission")
		}
	}

	var foundConfigLoad, foundPermissionNew bool
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if pkg.Name == "config" && sel.Sel.Name == "Load" {
			foundConfigLoad = true
		}
		if pkg.Name == "permission" && sel.Sel.Name == "New" {
			foundPermissionNew = true
		}
		return true
	})
	if foundConfigLoad {
		t.Error("internal/app/toolenv.go calls config.Load")
	}
	if foundPermissionNew {
		t.Error("internal/app/toolenv.go calls permission.New")
	}
}
