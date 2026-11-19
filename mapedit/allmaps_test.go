package mapedit

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"drishtimap/smap"
)

// Every real map file found under the folders in DRISHTI_MAPDIRS (separated by ;) must load, be
// written back byte for byte, and go through the description, structure check, detector and a
// no-change edit without an error. This is the test to run when maps from a new plant arrive.
func TestAllRealMaps(t *testing.T) {
	dirs := os.Getenv("DRISHTI_MAPDIRS")
	if dirs == "" {
		t.Skip("DRISHTI_MAPDIRS not set")
	}
	n, bad := 0, 0
	for _, d := range strings.Split(dirs, ";") {
		filepath.WalkDir(d, func(p string, e fs.DirEntry, err error) error {
			if err != nil || e.IsDir() || !strings.HasSuffix(strings.ToLower(p), ".smap") {
				return nil
			}
			n++
			raw, err := os.ReadFile(p)
			if err != nil {
				t.Errorf("%s: %v", p, err)
				return nil
			}
			m, err := smap.FromBytes(raw)
			if err != nil {
				bad++
				t.Errorf("CANNOT READ %s: %v", p, err)
				return nil
			}
			same := bytes.Equal(m.Bytes(), raw)
			if !same {
				bad++
				t.Errorf("NOT BYTE-IDENTICAL after read+write: %s", p)
			}
			i := Describe(m)
			st := Structure(m)
			fs := Detect(m)
			nf, nw := 0, 0
			for _, f := range fs {
				switch f.Level {
				case "fault":
					nf++
				case "warn":
					nw++
				}
			}
			// an edit that changes nothing real must leave a loadable file
			c := m.Clone()
			FixHeader(c, "")
			if _, err := smap.FromBytes(c.Bytes()); err != nil {
				bad++
				t.Errorf("%s: copy does not load: %v", p, err)
			}
			v := Verify(m, c)
			if !v.AllOK {
				bad++
				t.Errorf("%s: a map does not pass the checks against itself:\n%s", p, v.Text())
			}
			h := m.Root.O("header")
			t.Logf("%-70s %5.1f MB v%s name=%-18q walls %6d stations %3d %v paths %3d lines %4d areas %d bins %d refl %d | structure %d, detect %d fault %d warn",
				strings.TrimPrefix(p, d), i.SizeMB, h.S("version"), i.MapName, i.Walls, i.Stations, i.StationTypes, i.Paths, i.Lines, i.Areas, i.Bins, i.Reflectors, len(st), nf, nw)
			for k, s := range st {
				if k < 4 {
					t.Logf("      structure: %s", s)
				}
			}
			for _, f := range fs {
				if f.Level != "info" {
					t.Logf("      %s: %s", f.Level, f.Text)
				}
			}
			return nil
		})
	}
	t.Logf("%d map files, %d problems", n, bad)
	if n == 0 {
		t.Fatal("no .smap file found")
	}
}
