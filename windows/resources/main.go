// resources embeds the existing app icon, version and per-user Windows manifest.
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

func main() {
	version := flag.String("version", "0.3.0", "release version")
	windres := flag.String("windres", "windres.exe", "resource compiler")
	flag.Parse()
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(*version) {
		panic("numeric version required")
	}
	source, err := os.Open("cmd/modeluplink-app/icon.png")
	must(err)
	original, err := png.Decode(source)
	must(err)
	must(source.Close())
	icon := image.NewNRGBA(image.Rect(0, 0, 256, 256))
	bounds := original.Bounds()
	for y := 0; y < 256; y++ {
		for x := 0; x < 256; x++ {
			icon.Set(x, y, original.At(bounds.Min.X+x*bounds.Dx()/256, bounds.Min.Y+y*bounds.Dy()/256))
		}
	}
	var encoded bytes.Buffer
	must(png.Encode(&encoded, icon))
	var ico bytes.Buffer
	for _, v := range []uint16{0, 1, 1} {
		must(binary.Write(&ico, binary.LittleEndian, v))
	}
	ico.Write([]byte{0, 0, 0, 0})
	for _, v := range []uint16{1, 32} {
		must(binary.Write(&ico, binary.LittleEndian, v))
	}
	must(binary.Write(&ico, binary.LittleEndian, uint32(encoded.Len())))
	must(binary.Write(&ico, binary.LittleEndian, uint32(22)))
	ico.Write(encoded.Bytes())
	temp, err := os.MkdirTemp("", "modeluplink-resources-")
	must(err)
	defer os.RemoveAll(temp)
	must(os.WriteFile(filepath.Join(temp, "icon.ico"), ico.Bytes(), 0600))
	manifest := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0"><trustInfo xmlns="urn:schemas-microsoft-com:asm.v3"><security><requestedPrivileges><requestedExecutionLevel level="asInvoker" uiAccess="false"/></requestedPrivileges></security></trustInfo><compatibility xmlns="urn:schemas-microsoft-com:compatibility.v1"><application><supportedOS Id="{8e0f7a12-bfb3-4fe8-b9a5-48fd50a15a9a}"/></application></compatibility></assembly>`
	must(os.WriteFile(filepath.Join(temp, "app.manifest"), []byte(manifest), 0600))
	rc := fmt.Sprintf(`#include <windows.h>
1 ICON "icon.ico"
1 RT_MANIFEST "app.manifest"
1 VERSIONINFO
FILEVERSION %s,0
PRODUCTVERSION %s,0
FILEOS VOS_NT_WINDOWS32
FILETYPE VFT_APP
BEGIN
 BLOCK "StringFileInfo"
 BEGIN
  BLOCK "040904b0"
  BEGIN
   VALUE "CompanyName", "Model Uplink\0"
   VALUE "FileDescription", "Model Uplink\0"
   VALUE "FileVersion", "%s\0"
   VALUE "ProductName", "Model Uplink\0"
   VALUE "ProductVersion", "%s\0"
  END
 END
 BLOCK "VarFileInfo"
 BEGIN
  VALUE "Translation", 0x409, 1200
 END
END
`, strings.ReplaceAll(*version, ".", ","), strings.ReplaceAll(*version, ".", ","), *version, *version)
	must(os.WriteFile(filepath.Join(temp, "app.rc"), []byte(rc), 0600))
	for _, pkg := range []string{"cmd/modeluplink", "cmd/modeluplink-app"} {
		output, err := filepath.Abs(filepath.Join(pkg, "resource_windows_amd64.syso"))
		must(err)
		cmd := exec.Command(*windres, "--target=pe-x86-64", "-i", "app.rc", "-o", output)
		cmd.Dir = temp
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		must(cmd.Run())
	}
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
