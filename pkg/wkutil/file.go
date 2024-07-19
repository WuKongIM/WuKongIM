package wkutil

import (
	"io"
	"os"
)

// CopyFile CopyFile
func CopyFile(dstName, srcName string) (written int64, err error) {
	src, err := os.Open(srcName)
	if err != nil {
		return
	}
	defer src.Close()
	dst, err := os.OpenFile(dstName, os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return
	}
	defer dst.Close()
	return io.Copy(dst, src)
}

func WriteFile(filename string, data []byte) error {
	return os.WriteFile(filename, data, 0644)
}

func ReadFile(filename string) ([]byte, error) {
	return os.ReadFile(filename)

}

func FileExists(filename string) bool {
	_, err := os.Stat(filename)
	return err == nil || os.IsExist(err)
}

func RemoveFile(filename string) error {

	return os.Remove(filename)
}
