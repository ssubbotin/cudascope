package collector

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// argv builds /proc/<pid>/cmdline contents: every argument ends in a NUL.
func argv(args ...string) []byte {
	return []byte(strings.Join(args, "\x00") + "\x00")
}

func TestACommandLineReadsAsItsArguments(t *testing.T) {
	got := formatCmdline(argv("/venv/bin/python", "train.py", "--fold", "3"))
	if want := "/venv/bin/python train.py --fold 3"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestAnEmptyCommandLineIsEmpty(t *testing.T) {
	if got := formatCmdline(nil); got != "" {
		t.Fatalf("got %q, want nothing", got)
	}
}

func TestASecretAfterAnEqualsSignIsMasked(t *testing.T) {
	got := formatCmdline(argv("vllm", "serve", "--api-key=sk-live-123"))
	if want := "vllm serve --api-key=***"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestASecretInTheNextArgumentIsMasked(t *testing.T) {
	got := formatCmdline(argv("vllm", "serve", "--hf-token", "hf_abc", "--port", "8000"))
	if want := "vllm serve --hf-token *** --port 8000"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestASecretWithASpaceInItIsMaskedWhole(t *testing.T) {
	got := formatCmdline(argv("app", "--password", "correct horse battery"))
	if want := "app --password ***"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestAFlagFollowedByAnotherFlagMasksNothing(t *testing.T) {
	got := formatCmdline(argv("train", "--use-token-cache", "--fold", "3"))
	if want := "train --use-token-cache --fold 3"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestAnAssignmentToASecretIsMasked(t *testing.T) {
	got := formatCmdline(argv("env", "HF_TOKEN=hf_abc", "lr=3e-4", "python"))
	if want := "env HF_TOKEN=*** lr=3e-4 python"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSecretNamesAreMatchedWithoutRegardToCase(t *testing.T) {
	got := formatCmdline(argv("app", "--API-KEY", "abc", "Password=hunter2"))
	if want := "app --API-KEY *** Password=***"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestEveryNameThatSoundsSecretIsMasked(t *testing.T) {
	for _, flag := range []string{"--key", "--api-key", "--token", "--client-secret", "--password", "--passwd"} {
		got := formatCmdline(argv("app", flag, "value"))
		if want := "app " + flag + " ***"; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

func TestPasswordsInAURLAreMasked(t *testing.T) {
	got := formatCmdline(argv("worker", "--db=postgres://app:hunter2@db:5432/jobs"))
	if want := "worker --db=postgres://***@db:5432/jobs"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// A secret can sit inside one argument that holds a whole command: the code
// after python -c, or a shell line after sh -c.
func TestASecretInsideAnArgumentIsMasked(t *testing.T) {
	got := formatCmdline(argv("python", "-c", "import m; m.run(api_key='abc') --token xyz"))
	if want := "python -c import m; m.run(api_key=*** --token ***"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestALongCommandLineIsCut(t *testing.T) {
	got := formatCmdline(argv("python", strings.Repeat("ж", 2*maxCmdline)))
	if len(got) > maxCmdline+len("…") {
		t.Fatalf("got %d bytes, want at most %d", len(got), maxCmdline+len("…"))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("a cut line should say so, got %q", got)
	}
	if !utf8.ValidString(got) {
		t.Fatal("the cut split a character")
	}
}

func TestReadCmdlineReadsThisProcess(t *testing.T) {
	got := readCmdline(uint32(os.Getpid()))
	if !strings.Contains(got, filepath.Base(os.Args[0])) {
		t.Fatalf("got %q, want it to name %q", got, filepath.Base(os.Args[0]))
	}
}

func TestAGPUProcessCarriesItsCommandLine(t *testing.T) {
	p := newGPUProcess(1, nvml.ProcessInfo{Pid: uint32(os.Getpid()), UsedGpuMemory: 3 << 20}, 1700000000)
	if !strings.Contains(p.Cmdline, filepath.Base(os.Args[0])) {
		t.Fatalf("cmdline %q, want it to name %q", p.Cmdline, filepath.Base(os.Args[0]))
	}
	if p.GPUID != 1 || p.GPUMem != 3 || p.Timestamp != 1700000000 || p.Name == "" {
		t.Fatalf("got %+v", p)
	}
}

func TestAProcessThatIsGoneHasNoCommandLine(t *testing.T) {
	if got := readCmdline(0); got != "" {
		t.Fatalf("got %q, want nothing", got)
	}
}
