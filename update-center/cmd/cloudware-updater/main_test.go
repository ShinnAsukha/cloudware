package main

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mkManifest(tag, path, before, after string) manifest {
	return manifest{Schema: 1, Tag: tag, Services: []string{"web"}, Files: []patchFile{{Path: path, BeforeSHA256: before, SHA256: after}}}
}
func TestValidPaths(t *testing.T) {
	good := []string{"apps/web/app/page.tsx", "apps/api/main.go", "AI/CHANGELOG.md"}
	for _, p := range good {
		if !validRel(p) {
			t.Fatalf("valid path rejected %q", p)
		}
	}
	bad := []string{"", "apps/web/../api/bad", "apps/web/.env.local", "apps/web/foo\\bar", "apps/web/C:/x", "docker-compose.yml", "../apps/web/a", "apps/web/CON.txt", "apps/web/a.", "apps/web/node_modules/evil", "apps/web/a:key", "apps/web//oops", "apps/web/./a", "apps/web/secrets/t"}
	for _, p := range bad {
		if validRel(p) {
			t.Fatalf("unsafe path accepted %q", p)
		}
	}
}
func TestManifestRejectsUnsafe(t *testing.T) {
	tag := "cw-app-0.5.6"
	s := strings.Repeat("a", 64)
	ok := mkManifest(tag, "apps/web/app/test.tsx", "absent", s)
	if e := validateManifest(ok, tag); e != nil {
		t.Fatal(e)
	}
	cases := []struct {
		name   string
		modify func(*manifest)
	}{
		{"SQL", func(m *manifest) { m.DatabaseChanges = true }},
		{"invalid tag", func(m *manifest) { m.Tag = "../abc" }},
		{"wrong old sha", func(m *manifest) { m.Files[0].BeforeSHA256 = "zz" }},
		{"wrong new sha", func(m *manifest) { m.Files[0].SHA256 = "00" }},
		{"duplicate files", func(m *manifest) { m.Files = append(m.Files, m.Files[0]) }},
		{"missing service", func(m *manifest) { m.Services = nil }},
		{"invalid service", func(m *manifest) { m.Services = []string{"postgres"} }},
		{"path traversal", func(m *manifest) { m.Files[0].Path = "apps/web/../../.env" }},
		{"wrong schema", func(m *manifest) { m.Schema = 2 }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := ok
			m.Files = append([]patchFile(nil), ok.Files...)
			m.Services = append([]string(nil), ok.Services...)
			c.modify(&m)
			if validateManifest(m, tag) == nil {
				t.Fatal("expected error")
			}
		})
	}
}
func writePackage(t *testing.T, path string, m manifest, files map[string][]byte) {
	t.Helper()
	f, e := os.Create(path)
	if e != nil {
		t.Fatal(e)
	}
	w := zip.NewWriter(f)
	mbytes, _ := json.Marshal(m)
	z, e := w.Create("manifest.json")
	if e != nil {
		t.Fatal(e)
	}
	z.Write(mbytes)
	for p, b := range files {
		z, e = w.Create("payload/" + p)
		if e != nil {
			t.Fatal(e)
		}
		z.Write(b)
	}
	if e = w.Close(); e != nil {
		t.Fatal(e)
	}
	f.Close()
}
func TestPackagePositiveAndNegative(t *testing.T) {
	tag := "cw-app-0.5.6"
	p := "apps/web/app/CloudwareTest.tsx"
	contents := []byte("hello")
	m := mkManifest(tag, p, "absent", shaBytes(contents))
	dir := t.TempDir()
	zipFile := filepath.Join(dir, "good.zip")
	writePackage(t, zipFile, m, map[string][]byte{p: contents})
	got, e := loadPackage(zipFile, tag)
	if e != nil {
		t.Fatal(e)
	}
	if len(got.Data) != 1 {
		t.Fatal("unexpected data count")
	}
	root := filepath.Join(dir, "cloudware")
	os.MkdirAll(filepath.Join(root, "apps/web/app"), 0755)
	os.WriteFile(filepath.Join(root, "docker-compose.yml"), []byte("services: {}"), 0600)
	if e := preflight(root, got); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(root, filepath.FromSlash(p)), contents, 0600)
	if e := preflight(root, got); e == nil {
		t.Fatal("must reject unexpected preexisting source")
	}
	m.Files[0].SHA256 = strings.Repeat("b", 64)
	writePackage(t, zipFile, m, map[string][]byte{p: contents})
	if _, e := loadPackage(zipFile, tag); e == nil {
		t.Fatal("bad sha accepted")
	}
}
func TestPackageRejectsMissingExtraAndTraversal(t *testing.T) {
	tag := "cw-app-0.5.6"
	p := "apps/web/app/a.tsx"
	dir := t.TempDir()
	zipFile := filepath.Join(dir, "bad.zip")
	m := mkManifest(tag, p, "absent", shaBytes([]byte("a")))
	for _, tc := range []struct {
		name  string
		files map[string][]byte
	}{
		{"missing", map[string][]byte{}},
		{"extra", map[string][]byte{p: []byte("a"), "AI/added.txt": []byte("x")}},
		{"traversal", map[string][]byte{"apps/web/../../secrets": []byte("a")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writePackage(t, zipFile, m, tc.files)
			if _, e := loadPackage(zipFile, tag); e == nil {
				t.Fatal("malformed zip accepted")
			}
		})
	}
}
func TestSafetyAgainstSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	out := t.TempDir()
	os.MkdirAll(filepath.Join(root, "apps"), 0755)
	if e := os.Symlink(out, filepath.Join(root, "apps", "web")); e != nil {
		t.Skipf("symlink unavailable %v", e)
	}
	if _, e := ensureSafeTarget(root, "apps/web/app/new.tsx"); e == nil {
		t.Fatal("symlink escape allowed")
	}
}
func TestInstalledState(t *testing.T) {
	root := t.TempDir()
	if _, e := readInstalled(root); e != nil {
		t.Fatal(e)
	}
	os.MkdirAll(filepath.Join(root, "update-center"), 0755)
	os.WriteFile(filepath.Join(root, "update-center", "installed.json"), []byte(`{"tag":"../evil"}`), 0600)
	if _, e := readInstalled(root); e == nil {
		t.Fatal("unsafe installed state allowed")
	}
}

func TestVersions(t *testing.T) {
	if compareTags("cw-app-0.5.10", "cw-app-0.5.9") <= 0 || compareTags("cw-app-0.5.1", "cw-app-0.5.2") >= 0 || compareTags("cw-app-1.0.0", "cw-app-1.0.0") != 0 {
		t.Fatal("version ordering failed")
	}
}