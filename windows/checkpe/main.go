// checkpe verifies the distribution contract without executing foreign binaries.
package main

import (
	"debug/pe"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		panic("provide executable paths")
	}
	for _, path := range os.Args[1:] {
		f, err := pe.Open(path)
		if err != nil {
			panic(err)
		}
		h, ok := f.OptionalHeader.(*pe.OptionalHeader64)
		if !ok || f.Machine != pe.IMAGE_FILE_MACHINE_AMD64 || h.Subsystem != pe.IMAGE_SUBSYSTEM_WINDOWS_GUI {
			panic("expected x86-64 GUI executable: " + path)
		}
		_ = f.Close()
		fmt.Println(path + ": x86-64; no console subsystem")
	}
}
