package engine

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsRuntimeRejectsUnsafeArchivePaths(t *testing.T) {
	for _, name := range []string{"../escape.exe", `..\escape.exe`, "/absolute.exe", `C:\outside.exe`, "dll:stream", "trailing. /file"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "runtime.zip")
			f, _ := os.Create(path)
			z := zip.NewWriter(f)
			w, _ := z.Create(name)
			_, _ = w.Write([]byte("unsafe"))
			_ = z.Close()
			_ = f.Close()
			if err := extractWindowsRuntime(path, filepath.Join(root, "out")); err == nil {
				t.Fatalf("accepted %q", name)
			}
		})
	}
}
func TestWindowsRuntimeExtractsRuntimeAndGPULibraries(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "runtime.zip")
	f, _ := os.Create(path)
	z := zip.NewWriter(f)
	for _, name := range []string{"ollama.exe", "lib/ollama/cuda/runtime.dll"} {
		w, _ := z.Create(name)
		_, _ = w.Write([]byte(name))
	}
	_ = z.Close()
	_ = f.Close()
	dest := filepath.Join(root, "out")
	if err := extractWindowsRuntime(path, dest); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dest, "lib", "ollama", "cuda", "runtime.dll"))
	if err != nil || len(b) == 0 {
		t.Fatalf("missing runtime library: %v", err)
	}
}
