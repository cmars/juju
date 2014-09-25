// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package backups_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"

	gc "gopkg.in/check.v1"

	"github.com/juju/juju/testing"
)

type LegacySuite struct {
	testing.BaseSuite
}

type tarContent struct {
	Name   string
	Body   string
	Nested []tarContent
}

var expectedContents = map[string]string{
	"TarDirectoryEmpty":                     "",
	"TarDirectoryPopulated":                 "",
	"TarDirectoryPopulated/TarSubFile1":     "TarSubFile1",
	"TarDirectoryPopulated/TarSubDirectory": "",
	"TarFile1":                              "TarFile1",
	"TarFile2":                              "TarFile2",
}

func (s *LegacySuite) createTestFiles(c *gc.C) (string, []string, []tarContent) {
	var expected []tarContent
	var tempFiles []string

	rootDir := c.MkDir()

	for name, body := range expectedContents {
		filename := filepath.Join(rootDir, filepath.FromSlash(name))

		top := (path.Dir(name) == ".")

		if body == "" {
			err := os.MkdirAll(filename, os.FileMode(0755))
			c.Check(err, gc.IsNil)
		} else {
			if !top {
				err := os.MkdirAll(filepath.Dir(filename), os.FileMode(0755))
				c.Check(err, gc.IsNil)
			}
			file, err := os.Create(filename)
			c.Assert(err, gc.IsNil)
			file.WriteString(body)
			file.Close()
		}

		if top {
			tempFiles = append(tempFiles, filename)
		}
		content := tarContent{filepath.ToSlash(filename), body, nil}
		expected = append(expected, content)
	}

	return rootDir, tempFiles, expected
}

func readTarFile(c *gc.C, tarFile io.Reader) map[string]string {
	tr := tar.NewReader(tarFile)
	contents := make(map[string]string)

	// Iterate through the files in the archive.
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			// end of tar archive
			break
		}
		c.Assert(err, gc.IsNil)
		buf, err := ioutil.ReadAll(tr)
		c.Assert(err, gc.IsNil)
		contents[hdr.Name] = string(buf)
	}

	return contents
}

func (s *LegacySuite) checkTarContents(
	c *gc.C, tarFile io.Reader, allExpected []tarContent,
) {
	contents := readTarFile(c, tarFile)

	// Check that the expected entries are there.
	// XXX Check for unexpected entries.
	for _, expected := range allExpected {
		relPath := strings.TrimPrefix(expected.Name, string(os.PathSeparator))

		c.Log(fmt.Sprintf("checking for presence of %q on tar file", relPath))
		body, found := contents[relPath]
		if !found {
			c.Errorf("%q not found", expected.Name)
			continue
		}

		if expected.Body != "" {
			c.Log("Also checking the file contents")
			c.Check(body, gc.Equals, expected.Body)
		}

		if expected.Nested != nil {
			c.Log("Also checking the nested tar file")
			nestedFile := bytes.NewBufferString(body)
			s.checkTarContents(c, nestedFile, expected.Nested)
		}
	}

	if c.Failed() {
		c.Log("-----------------------")
		c.Log("expected:")
		for _, expected := range allExpected {
			c.Log(fmt.Sprintf("%s -> %q", expected.Name, expected.Body))
		}
		c.Log("got:")
		for name, body := range contents {
			if len(body) > 200 {
				body = body[:200] + "...(truncated)"
			}
			c.Log(fmt.Sprintf("%s -> %q", name, body))
		}
	}
}

func (s *LegacySuite) checkChecksum(c *gc.C, file *os.File, checksum string) {
	fileShaSum := shaSumFile(c, file)
	c.Check(fileShaSum, gc.Equals, checksum)
	resetFile(c, file)
}

func (s *LegacySuite) checkSize(c *gc.C, file *os.File, size int64) {
	stat, err := file.Stat()
	c.Assert(err, gc.IsNil)
	c.Check(stat.Size(), gc.Equals, size)
}

func (s *LegacySuite) checkArchive(c *gc.C, file *os.File, bundle []tarContent) {
	expected := []tarContent{
		{"juju-backup", "", nil},
		{"juju-backup/dump", "", nil},
		{"juju-backup/root.tar", "", bundle},
	}

	tarFile, err := gzip.NewReader(file)
	c.Assert(err, gc.IsNil)

	s.checkTarContents(c, tarFile, expected)
	resetFile(c, file)
}

func resetFile(c *gc.C, reader io.Reader) {
	file, ok := reader.(*os.File)
	c.Assert(ok, gc.Equals, true)
	_, err := file.Seek(0, os.SEEK_SET)
	c.Assert(err, gc.IsNil)
}
