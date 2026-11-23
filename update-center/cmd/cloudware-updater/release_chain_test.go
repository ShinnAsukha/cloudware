package main

import (
    "os"
    "path/filepath"
    "strings"
    "testing"
)

func TestReleasePlanOrderAndFilters(t *testing.T) {
    releases := []release{
        {TagName: "cw-app-0.6.0"}, {TagName: "cw-app-0.5.11"},
        {TagName: "cw-app-0.5.10"}, {TagName: "cw-app-0.5.8"},
        {TagName: "cw-app-0.7.0", IsDraft: true},
        {TagName: "cw-app-0.8.0", IsPrerelease: true},
        {TagName: "invalid"},
    }
    got, err := pendingTags(releases, "cw-app-0.5.8")
    if err != nil { t.Fatal(err) }
    want := []string{"cw-app-0.5.10", "cw-app-0.5.11", "cw-app-0.6.0"}
    if len(got) != len(want) { t.Fatalf("got %v", got) }
    for i := range want {
        if got[i] != want[i] { t.Fatalf("got %v want %v", got, want) }
    }
    all, err := pendingTags(releases, "")
    if err != nil || len(all) != 4 { t.Fatalf("new installation: %v %v", all, err) }
    current, err := pendingTags(releases, "cw-app-0.6.0")
    if err != nil || len(current) != 0 { t.Fatalf("latest: %v %v", current, err) }
}
func TestReleasePlanFailsClosed(t *testing.T) {
    if _, err := pendingTags([]release{{TagName:"cw-app-0.5.5"}, {TagName:"cw-app-0.5.5"}}, ""); err == nil {
        t.Fatal("duplicate release accepted")
    }
    if _, err := pendingTags([]release{{TagName:"cw-app-0.5.1"}, {TagName:"cw-app-0.5.01"}}, ""); err == nil {
        t.Fatal("semantically duplicated release accepted")
    }
    if _, err := pendingTags(nil, "../secret"); err == nil { t.Fatal("invalid local tag accepted") }
    many := []release{}
    for i := 1; i < 30; i++ {
        many = append(many, release{TagName: "cw-app-0.9."+itoa(i)})
    }
    if _, err := pendingTags(many, ""); err == nil { t.Fatal("more than 25 updates accepted") }
}
func itoa(i int) string {
    if i == 0 { return "0" }
    b := []byte{}
    for i > 0 { b = append([]byte{byte('0'+i%10)}, b...); i /= 10 }
    return string(b)
}
func chainPkg(tag, path, before, content string) pkg {
    b := []byte(content)
    return pkg{
        Manifest: manifest{
            Schema: 1, Tag: tag, Services: []string{"web"},
            Files: []patchFile{{Path: path, BeforeSHA256: before, SHA256: shaBytes(b)}},
        },
        Data: map[string][]byte{path: b},
    }
}
func TestCombinePackagesAndRealPreflight(t *testing.T) {
    path := "apps/web/app/NoticeL56.tsx"
    first := chainPkg("cw-app-0.5.6", path, "absent", "v1")
    second := chainPkg("cw-app-0.5.7", path, shaBytes([]byte("v1")), "v2")
    merged, err := combinePackages([]pkg{first, second})
    if err != nil { t.Fatal(err) }
    if merged.Manifest.Tag != "cw-app-0.5.7" || len(merged.Manifest.Files) != 1 ||
       merged.Manifest.Files[0].BeforeSHA256 != "absent" || string(merged.Data[path]) != "v2" {
        t.Fatalf("merged result invalid: %+v", merged.Manifest)
    }
    root := t.TempDir()
    if err := os.MkdirAll(filepath.Join(root, "apps", "web", "app"), 0755); err != nil { t.Fatal(err) }
    if err := os.WriteFile(filepath.Join(root, "docker-compose.yml"), []byte("services: {}"), 0600); err != nil { t.Fatal(err) }
    if err := preflight(root, merged); err != nil { t.Fatalf("combined preflight: %v", err) }
    if err := os.WriteFile(filepath.Join(root, "apps", "web", "app", "NoticeL56.tsx"), []byte("unexpected"), 0600); err != nil {t.Fatal(err)}
    if err := preflight(root, merged); err == nil { t.Fatal("unexpected current source accepted") }
}
func TestCombinePackagesRejectBrokenChain(t *testing.T) {
    path := "apps/web/app/NoticeL56.tsx"
    first := chainPkg("cw-app-0.5.6", path, "absent", "v1")
    bad := chainPkg("cw-app-0.5.7", path, strings.Repeat("a",64), "v2")
    if _, err := combinePackages([]pkg{first, bad}); err == nil {
        t.Fatal("broken before SHA chain accepted")
    }
    outOfOrder := chainPkg("cw-app-0.5.5", path, shaBytes([]byte("v1")), "v0")
    if _, err := combinePackages([]pkg{first, outOfOrder}); err == nil {
        t.Fatal("out-of-order versions accepted")
    }
    if _, err := combinePackages(nil); err == nil { t.Fatal("empty chain accepted") }
    other := chainPkg("cw-app-0.5.7", "apps/web/app/noticel56.tsx", "absent", "v2")
    if _, err := combinePackages([]pkg{first, other}); err == nil {
        t.Fatal("case alias accepted")
    }
}
