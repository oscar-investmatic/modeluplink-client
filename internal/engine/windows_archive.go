package engine

import (
	"archive/zip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// extractWindowsRuntime rejects paths which can escape the verified runtime
// directory, NTFS alternate streams, links, and oversized expanded archives.
func extractWindowsRuntime(archive, destination string) error {
	reader, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer reader.Close()
	var expanded uint64
	for _, file := range reader.File {
		name := strings.ReplaceAll(file.Name, `\`, "/")
		if strings.Contains(name, ":") || strings.HasPrefix(name, "/") || strings.Contains(name, "\x00") || file.Mode()&os.ModeSymlink != 0 {
			return errors.New("unsafe path in Windows runtime")
		}
		for _, part := range strings.Split(name, "/") {
			stem := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
			reserved := stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || (len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && strings.ContainsRune("123456789¹²³", rune(stem[3])))
			if reserved || part == ".." || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
				return errors.New("unsafe path in Windows runtime")
			}
		}
		path := filepath.Join(destination, filepath.FromSlash(name))
		if file.FileInfo().IsDir() {
			if err = os.MkdirAll(path, 0700); err != nil {
				return err
			}
			continue
		}
		expanded += file.UncompressedSize64
		if expanded > 8<<30 || file.UncompressedSize64 > 2<<30 {
			return errors.New("expanded Windows runtime exceeds size limit")
		}
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		src, err := file.Open()
		if err != nil {
			return err
		}
		dst, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			src.Close()
			return err
		}
		n, copyErr := io.Copy(dst, io.LimitReader(src, int64(file.UncompressedSize64)+1))
		closeErr := dst.Close()
		src.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if uint64(n) != file.UncompressedSize64 {
			return errors.New("invalid Windows runtime entry size")
		}
	}
	return nil
}
