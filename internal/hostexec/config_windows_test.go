package hostexec

import "testing"

func TestHelpersNeverOpenAConsole(t *testing.T) {
	cmd := Command("modeluplink.exe", "_desktop")
	if !cmd.SysProcAttr.HideWindow || cmd.SysProcAttr.CreationFlags&0x08000000 == 0 {
		t.Fatal("helper may open console")
	}
	if len(cmd.Args) != 2 {
		t.Fatal("unexpected helper arguments")
	}
}
