package host

import "testing"

const snapSrc = `
class Handler:
    def __init__(self, cfg):
        self.cfg = cfg
    def run(self, payload):
        return {"id": payload.get("id"), "cfg": self.cfg}

_h = Handler({"mode": "prod", "retries": 3})

def handle(payload):
    return _h.run(payload)
`

func loaded(t *testing.T) (*ABI, *Snapshot) {
	t.Helper()
	a, err := New(0)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Eval(snapSrc, ModeExec); err != nil {
		t.Fatal(err)
	}
	snap, err := a.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	return a, snap
}

func TestRestore(t *testing.T) {
	parent, snap := loaded(t)

	child, err := snap.Restore()
	if err != nil {
		t.Fatal(err)
	}

	handle, err := child.Func("handle")
	if err != nil {
		t.Fatalf("restored interpreter lost handle: %v", err)
	}
	got, err := child.Call(handle, []any{map[string]any{"id": "x1"}})
	if err != nil {
		t.Fatal(err)
	}

	// Reaches _h, which only exists because the snapshot captured the objects
	// the source built, not just its code.
	want := map[string]any{"id": "x1", "cfg": map[string]any{"mode": "prod", "retries": int64(3)}}
	if m, ok := got.(map[string]any); !ok || m["id"] != want["id"] {
		t.Errorf("handle() = %#v, want %#v", got, want)
	}

	if err := child.Eval("marker = 1", ModeExec); err != nil {
		t.Fatal(err)
	}
	if err := parent.Eval("marker", ModeValue); err == nil {
		t.Error("parent can see the child's global")
	}
	if err := child.Eval("marker", ModeValue); err != nil {
		t.Errorf("child lost its own global: %v", err)
	}
}

func TestRestoreInto(t *testing.T) {
	a, snap := loaded(t)

	if err := a.Eval("marker = 1", ModeExec); err != nil {
		t.Fatal(err)
	}
	if err := a.RestoreInto(snap); err != nil {
		t.Fatal(err)
	}

	if err := a.Eval("marker", ModeValue); err == nil {
		t.Error("marker survived the restore")
	}
	if _, err := a.Func("handle"); err != nil {
		t.Errorf("restore lost the snapshot's definitions: %v", err)
	}
}

func TestSnapshotRejectsMidCall(t *testing.T) {
	a, _ := loaded(t)

	*a.mod.X__stack_pointer() = a.base - 16
	defer func() { *a.mod.X__stack_pointer() = a.base }()

	if _, err := a.Snapshot(); err == nil {
		t.Error("Snapshot accepted a stack that was not at its base")
	}
}
