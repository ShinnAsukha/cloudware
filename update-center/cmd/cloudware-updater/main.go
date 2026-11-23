// Cloudware Update Center: explicit-approval GitHub release updater.
// This program never executes downloaded scripts or SQL, and never auto-installs.
package main

import (
	"archive/zip"
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const repo = "ShinnAsukha/cloudware"
const trustedWorkflow = "ShinnAsukha/cloudware/.github/workflows/cloudware-release.yml"
const packageName = "cloudware-update.zip"
const defaultRoot = `E:\Cloudware`

var tagRx = regexp.MustCompile(`^cw-app-[0-9]+\.[0-9]+\.[0-9]+$`)
var shaRx = regexp.MustCompile(`^[a-f0-9]{64}$`)

type patchFile struct {
	Path         string `json:"path"`
	BeforeSHA256 string `json:"before_sha256"` // 64 hex or "absent"
	SHA256       string `json:"sha256"`
}
type manifest struct {
	Schema          int         `json:"schema"`
	Tag             string      `json:"tag"`
	DatabaseChanges bool        `json:"database_changes"`
	Services        []string    `json:"services"`
	Files           []patchFile `json:"files"`
	Notes           string      `json:"notes"`
}
type release struct {
	TagName      string `json:"tagName"`
	IsDraft      bool   `json:"isDraft"`
	IsPrerelease bool   `json:"isPrerelease"`
}
type installed struct {
	Tag         string `json:"tag"`
	InstalledAt string `json:"installed_at"`
	Backup      string `json:"backup"`
}
type backupEntry struct {
	Path    string `json:"path"`
	Existed bool   `json:"existed"`
}
type backupLedger struct {
	Tag      string        `json:"tag"`
	Entries  []backupEntry `json:"entries"`
	Services []string      `json:"services"`
}

func main() {
	fmt.Println("Cloudware Update Center | GitHub Private | Onayli Kurulum")
	mode := "check"
	if len(os.Args) > 1 {
		mode = strings.ToLower(os.Args[1])
	}
	root := os.Getenv("CLOUDWARE_ROOT")
	if root == "" {
		root = defaultRoot
	}
	if len(os.Args) > 2 {
		root = os.Args[2]
	}
	root, _ = filepath.Abs(root)
	if err := run(mode, root); err != nil {
		fmt.Fprintln(os.Stderr, "[FAIL]", err)
		os.Exit(1)
	}
}

func run(mode, root string) error {
	switch mode {
	case "help":
		fmt.Println("Komutlar: check | download | install | status | help")
		fmt.Println("Varsayilan ana klasor:", defaultRoot)
		return nil
	case "status":
		curr, err := readInstalled(root)
		if err != nil {
			return err
		}
		if curr.Tag == "" {
			fmt.Println("Kurulmus Update Center uygulama surumu kaydi yok.")
		} else {
			fmt.Println("Kurulu uygulama surumu:", curr.Tag, "\nYedek:", curr.Backup)
		}
		return nil
	case "check", "download", "install":
	default:
		return fmt.Errorf("bilinmeyen komut: %s", mode)
	}
	if _, err := exec.LookPath("gh"); err != nil {
		return errors.New("GitHub CLI (gh) kurulu degil. https://cli.github.com adresinden kurun, sonra 'gh auth login' yapin")
	}
	if _, err := command("gh", "auth", "status", "--hostname", "github.com"); err != nil {
		return fmt.Errorf("GitHub hesabi bagli degil: gh auth login (%w)", err)
	}
	if _, err := command("gh", "repo", "view", repo, "--json", "nameWithOwner,visibility"); err != nil {
		return fmt.Errorf("private depoya erisim yok: %w", err)
	}
	raw, err := command("gh", "repo", "view", repo, "--json", "nameWithOwner,visibility")
	if err != nil {
		return err
	}
	var repoInfo struct {
		NameWithOwner string `json:"nameWithOwner"`
		Visibility    string `json:"visibility"`
	}
	if err := json.Unmarshal(raw, &repoInfo); err != nil {
		return err
	}
	if repoInfo.NameWithOwner != repo || !strings.EqualFold(repoInfo.Visibility, "private") {
		return errors.New("depo private degil veya yanlis depo")
	}
	data, err := command("gh", "release", "list", "--repo", repo, "--json", "tagName,isDraft,isPrerelease", "--limit", "100")
	if err != nil {
		return fmt.Errorf("release kontrolu basarisiz: %w", err)
	}
	var releases []release
	if err := json.Unmarshal(data, &releases); err != nil {
		return err
	}
	var latest string
	// gh returns most recently published releases first. Never select a prerelease or draft.
	for _, rel := range releases {
		if !rel.IsDraft && !rel.IsPrerelease && tagRx.MatchString(rel.TagName) {
			latest = rel.TagName
			break
		}
	}
	if latest == "" {
		fmt.Println("[OK] GitHub baglantisi aktif. Cloudware icin henuz onayli uygulama surumu yayinlanmadi.")
		return nil
	}
	curr, err := readInstalled(root)
	if err != nil {
		return err
	}
	if curr.Tag != "" && compareTags(latest, curr.Tag) <= 0 {
		fmt.Println("[OK] Cloudware guncel:", latest)
		return nil
	}
	fmt.Printf("[NEW] %s  (kurulu: %s)\n", latest, showTag(curr.Tag))
	if mode == "check" {
		fmt.Println("Kurulum icin Update Center'i acip 'download' ve 'install' secin. Otomatik kurulum YOK.")
		return nil
	}
	if _, err := os.Stat(root); err != nil {
		return fmt.Errorf("Cloudware proje klasoru yok: %s", root)
	}
	stage, err := stageDir(latest)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(stage, 0700); err != nil {
		return err
	}
	archive := filepath.Join(stage, packageName)
	// Downloads in a dedicated empty directory, never in the repository or app root.
	if err := os.RemoveAll(stage); err != nil {
		return err
	}
	if err := os.MkdirAll(stage, 0700); err != nil {
		return err
	}
	if _, err := command("gh", "release", "download", latest, "--repo", repo, "--pattern", packageName, "--dir", stage); err != nil {
		return fmt.Errorf("indirme basarisiz: %w", err)
	}
	// Release asset MUST have provenance from the particular allowed workflow.
	if _, err := command("gh", "attestation", "verify", archive, "--repo", repo, "--signer-workflow", trustedWorkflow); err != nil {
		return fmt.Errorf("paket kaynagi/imzasi dogrulanamadi (kurulum ENGELLENDI): %w", err)
	}
	pack, err := loadPackage(archive, latest)
	if err != nil {
		return err
	}
	fmt.Printf("[PASS] Imza, manifest ve %d dosya dogrulandi.\n", len(pack.Manifest.Files))
	if err := preflight(root, pack); err != nil {
		return fmt.Errorf("kaynak uyumu: %w", err)
	}
	fmt.Println("[PASS] Canli kaynak SHA256 kontrolleri.")
	if mode == "download" {
		fmt.Println("[READY] Paket sadece kontrol edildi. Hicbir uygulama dosyasi degistirilmedi.")
		return nil
	}
	fmt.Printf("\nBu surum %d dosyayi degistirecek, servisler: %s\n", len(pack.Manifest.Files), strings.Join(pack.Manifest.Services, ","))
	fmt.Printf("Devam etmek icin tam olarak KUR %s yazin: ", latest)
	answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if strings.TrimSpace(answer) != "KUR "+latest {
		return errors.New("kullanici onayi verilmedi; islem iptal")
	}
	if err := apply(root, pack); err != nil {
		return err
	}
	fmt.Println("[PASS] Kaynaklar guncellendi, Docker servisleri kontrol edildi, surum kaydedildi.")
	return nil
}

func compareTags(a, b string) int {
	var ax, ay, az, bx, by, bz int
	fmt.Sscanf(strings.TrimPrefix(a, "cw-app-"), "%d.%d.%d", &ax, &ay, &az)
	fmt.Sscanf(strings.TrimPrefix(b, "cw-app-"), "%d.%d.%d", &bx, &by, &bz)
	for _, p := range [][2]int{{ax, bx}, {ay, by}, {az, bz}} {
		if p[0] < p[1] {
			return -1
		}
		if p[0] > p[1] {
			return 1
		}
	}
	return 0
}
func showTag(s string) string {
	if s == "" {
		return "bilinmiyor"
	}
	return s
}
func stageDir(tag string) (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "Cloudware", "UpdateCenter", "staging", tag), nil
}
func readInstalled(root string) (installed, error) {
	var v installed
	data, err := os.ReadFile(filepath.Join(root, "update-center", "installed.json"))
	if errors.Is(err, os.ErrNotExist) {
		return v, nil
	}
	if err != nil {
		return v, err
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return v, fmt.Errorf("kurulu surum kaydi bozuk: %w", err)
	}
	if v.Tag != "" && !tagRx.MatchString(v.Tag) {
		return v, errors.New("kurulu surum kaydi guvensiz")
	}
	return v, nil
}
func command(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}
func shaBytes(b []byte) string { hash := sha256.Sum256(b); return hex.EncodeToString(hash[:]) }
func shaFile(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func validRel(s string) bool {
	if s == "" || len(s) > 240 || strings.Contains(s, "\\") || strings.HasPrefix(s, "/") {
		return false
	}
	allowed := strings.HasPrefix(s, "apps/web/") || strings.HasPrefix(s, "apps/api/") || strings.HasPrefix(s, "AI/")
	if !allowed {
		return false
	}
	for _, part := range strings.Split(s, "/") {
		if part == "" || part == "." || part == ".." || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") || strings.ContainsAny(part, `:*?"<>|`) {
			return false
		}
		lo := strings.ToLower(part)
		if lo == ".git" || lo == "node_modules" || lo == ".next" || lo == "secrets" || strings.HasPrefix(lo, ".env") || strings.HasSuffix(lo, ".pem") || strings.HasSuffix(lo, ".key") || strings.HasSuffix(lo, ".pfx") {
			return false
		}
		stem := strings.Split(lo, ".")[0]
		if stem == "con" || stem == "prn" || stem == "aux" || stem == "nul" || regexp.MustCompile(`^(com|lpt)[1-9]$`).MatchString(stem) {
			return false
		}
	}
	return true
}
func validateManifest(m manifest, tag string) error {
	if m.Schema != 1 || m.Tag != tag || !tagRx.MatchString(tag) || m.DatabaseChanges {
		return errors.New("manifest surum/schema veya SQL degisikligi engellendi")
	}
	if len(m.Files) == 0 || len(m.Files) > 400 {
		return errors.New("dosya sayisi gecersiz")
	}
	svcs := map[string]bool{}
	for _, s := range m.Services {
		if (s != "web" && s != "api") || svcs[s] {
			return errors.New("izin verilmeyen/tekrarlanan servis")
		}
		svcs[s] = true
	}
	found := map[string]bool{}
	for _, f := range m.Files {
		if !validRel(f.Path) || !shaRx.MatchString(f.SHA256) || (f.BeforeSHA256 != "absent" && !shaRx.MatchString(f.BeforeSHA256)) {
			return fmt.Errorf("manifest dosya/hash hatasi: %s", f.Path)
		}
		key := strings.ToLower(f.Path)
		if found[key] {
			return fmt.Errorf("tekrarlanan dosya: %s", f.Path)
		}
		found[key] = true
		if strings.HasPrefix(f.Path, "apps/web/") && !svcs["web"] {
			return errors.New("web degisikligi icin web servisi gerekli")
		}
		if strings.HasPrefix(f.Path, "apps/api/") && !svcs["api"] {
			return errors.New("api degisikligi icin api servisi gerekli")
		}
	}
	return nil
}

type pkg struct {
	Manifest manifest
	Data     map[string][]byte
}

func loadPackage(archive, tag string) (pkg, error) {
	var p pkg
	p.Data = map[string][]byte{}
	fi, err := os.Stat(archive)
	if err != nil {
		return p, err
	}
	if fi.Size() > 100<<20 {
		return p, errors.New("ZIP boyutu cok buyuk")
	}
	r, err := zip.OpenReader(archive)
	if err != nil {
		return p, err
	}
	defer r.Close()
	if len(r.File) > 402 {
		return p, errors.New("ZIP cok fazla dosya iceriyor")
	}
	seen := map[string]bool{}
	var manifestData []byte
	var total uint64
	for _, f := range r.File {
		name := f.Name
		if name != "manifest.json" && !(strings.HasPrefix(name, "payload/") && validRel(strings.TrimPrefix(name, "payload/"))) {
			return p, fmt.Errorf("izin verilmeyen ZIP yolu: %s", name)
		}
		if !f.Mode().IsRegular() {
			return p, fmt.Errorf("ZIP symlink/ozel dosya yasak: %s", name)
		}
		if f.UncompressedSize64 > 64<<20 {
			return p, errors.New("tek dosya siniri asildi")
		}
		total += f.UncompressedSize64
		if total > 256<<20 {
			return p, errors.New("ZIP acilmis boyut siniri asildi")
		}
		k := strings.ToLower(name)
		if seen[k] {
			return p, errors.New("ZIP tekrarlanan dosya")
		}
		seen[k] = true
		reader, e := f.Open()
		if e != nil {
			return p, e
		}
		b, e := io.ReadAll(io.LimitReader(reader, int64(f.UncompressedSize64)+1))
		reader.Close()
		if e != nil || uint64(len(b)) != f.UncompressedSize64 {
			return p, errors.New("ZIP boyut tutarsizligi")
		}
		if name == "manifest.json" {
			manifestData = b
		} else {
			p.Data[strings.TrimPrefix(name, "payload/")] = b
		}
	}
	if len(manifestData) == 0 || len(manifestData) > 256<<10 {
		return p, errors.New("manifest bulunamadi/buyuk")
	}
	if err := json.Unmarshal(manifestData, &p.Manifest); err != nil {
		return p, err
	}
	if err := validateManifest(p.Manifest, tag); err != nil {
		return p, err
	}
	if len(p.Data) != len(p.Manifest.Files) {
		return p, errors.New("manifest ZIP dosya sayisi uyusmuyor")
	}
	for _, file := range p.Manifest.Files {
		b, ok := p.Data[file.Path]
		if !ok || shaBytes(b) != file.SHA256 {
			return p, fmt.Errorf("ZIP SHA256 uyusmuyor: %s", file.Path)
		}
	}
	return p, nil
}
func ensureSafeTarget(root, rel string) (string, error) {
	if !validRel(rel) {
		return "", errors.New("izin verilmeyen dosya yolu")
	}
	absRoot, e := filepath.Abs(root)
	if e != nil {
		return "", e
	}
	resolvedRoot, e := filepath.EvalSymlinks(absRoot)
	if e != nil {
		return "", e
	}
	target := filepath.Join(absRoot, filepath.FromSlash(rel))
	parent := filepath.Dir(target)
	for {
		info, e := os.Lstat(parent)
		if e == nil {
			if !info.IsDir() {
				return "", errors.New("hedef dizin degil")
			}
			r, e := filepath.EvalSymlinks(parent)
			if e != nil {
				return "", e
			}
			back, e := filepath.Rel(resolvedRoot, r)
			if e != nil || back == ".." || strings.HasPrefix(back, ".."+string(filepath.Separator)) {
				return "", errors.New("hedef reparse/junction disari tasiyor")
			}
			break
		}
		if !errors.Is(e, os.ErrNotExist) {
			return "", e
		}
		if parent == filepath.Dir(parent) {
			return "", errors.New("kok bulunamadi")
		}
		parent = filepath.Dir(parent)
	}
	if info, e := os.Lstat(target); e == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("hedef regular file degil")
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return "", e
	}
	return target, nil
}
func preflight(root string, p pkg) error {
	fi, e := os.Stat(root)
	if e != nil || !fi.IsDir() {
		return errors.New("Cloudware ana klasoru bulunamadi")
	}
	compose, e := findCompose(root)
	if e != nil {
		return e
	}
	_ = compose
	for _, f := range p.Manifest.Files {
		target, e := ensureSafeTarget(root, f.Path)
		if e != nil {
			return e
		}
		hash, e := shaFile(target)
		if f.BeforeSHA256 == "absent" {
			if e == nil {
				return fmt.Errorf("zaten var, uzerine yazilmiyor: %s", f.Path)
			}
			if !errors.Is(e, os.ErrNotExist) {
				return e
			}
		} else if e != nil || hash != f.BeforeSHA256 {
			return fmt.Errorf("mevcut kaynak SHA256 uyumsuz: %s", f.Path)
		}
	}
	return nil
}
func findCompose(root string) (string, error) {
	for _, x := range []string{"docker-compose.yml", "docker-compose.yaml", "compose.yaml", "compose.yml"} {
		if fi, e := os.Stat(filepath.Join(root, x)); e == nil && fi.Mode().IsRegular() {
			return x, nil
		}
	}
	return "", errors.New("Docker Compose dosyasi yok; devam edilmiyor")
}
func writeAtomically(path string, data []byte) error {
	if e := os.MkdirAll(filepath.Dir(path), 0755); e != nil {
		return e
	}
	tmp := path + ".cw-tmp"
	if e := os.WriteFile(tmp, data, 0600); e != nil {
		return e
	}
	if e := os.Rename(tmp, path); e != nil { // Windows cannot rename over an existing file.
		if e2 := os.Remove(path); e2 != nil && !errors.Is(e2, os.ErrNotExist) {
			os.Remove(tmp)
			return e2
		}
		if e2 := os.Rename(tmp, path); e2 != nil {
			os.Remove(tmp)
			return e2
		}
	}
	return nil
}
func apply(root string, p pkg) (outErr error) {
	if err := preflight(root, p); err != nil {
		return err
	}
	stamp := time.Now().UTC().Format("20060102T150405.000000000")
	backupRoot := filepath.Join(root, "update-center", "backups", stamp)
	if err := os.MkdirAll(backupRoot, 0700); err != nil {
		return err
	}
	ledger := backupLedger{Tag: p.Manifest.Tag, Services: p.Manifest.Services}
	for _, f := range p.Manifest.Files {
		target, _ := ensureSafeTarget(root, f.Path)
		data, e := os.ReadFile(target)
		exists := e == nil
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
		if exists {
			copyPath := filepath.Join(backupRoot, "files", filepath.FromSlash(f.Path))
			if e := os.MkdirAll(filepath.Dir(copyPath), 0700); e != nil {
				return e
			}
			if e := os.WriteFile(copyPath, data, 0600); e != nil {
				return e
			}
		}
		ledger.Entries = append(ledger.Entries, backupEntry{Path: f.Path, Existed: exists})
	}
	ledgerBytes, _ := json.MarshalIndent(ledger, "", "  ")
	if err := os.WriteFile(filepath.Join(backupRoot, "ledger.json"), ledgerBytes, 0600); err != nil {
		return err
	}
	modified := false
	defer func() {
		if outErr == nil || !modified {
			return
		}
		fmt.Fprintln(os.Stderr, "[ROLLBACK] Hata olustu, eski kaynaklar geri yukleniyor")
		errs := []string{}
		for _, f := range ledger.Entries {
			dst, e := ensureSafeTarget(root, f.Path)
			if e != nil {
				errs = append(errs, e.Error())
				continue
			}
			if f.Existed {
				b, e := os.ReadFile(filepath.Join(backupRoot, "files", filepath.FromSlash(f.Path)))
				if e == nil {
					e = writeAtomically(dst, b)
				}
				if e != nil {
					errs = append(errs, e.Error())
				}
			} else {
				if e := os.Remove(dst); e != nil && !errors.Is(e, os.ErrNotExist) {
					errs = append(errs, e.Error())
				}
			}
		}
		if len(ledger.Services) > 0 {
			if e := deploy(root, ledger.Services); e != nil {
				errs = append(errs, "servis geri alma: "+e.Error())
			}
		}
		if len(errs) > 0 {
			outErr = fmt.Errorf("%w; ROLLBACK TAMAMLANAMADI: %s", outErr, strings.Join(errs, " | "))
		} else {
			fmt.Fprintln(os.Stderr, "[ROLLBACK] Kaynaklar geri yuklendi; servis kontrol edildi")
		}
	}()
	for _, f := range p.Manifest.Files {
		target, e := ensureSafeTarget(root, f.Path)
		if e != nil {
			return e
		}
		modified = true
		if e := writeAtomically(target, p.Data[f.Path]); e != nil {
			return e
		}
	}
	if len(p.Manifest.Services) > 0 {
		if e := deploy(root, p.Manifest.Services); e != nil {
			return e
		}
	}
	old, e := readInstalled(root)
	if e != nil {
		return e
	}
	_ = old
	installedBytes, _ := json.MarshalIndent(installed{Tag: p.Manifest.Tag, InstalledAt: time.Now().UTC().Format(time.RFC3339), Backup: backupRoot}, "", "  ")
	if err := writeAtomically(filepath.Join(root, "update-center", "installed.json"), installedBytes); err != nil {
		return err
	}
	return nil
}
func deploy(root string, services []string) error {
	compose, e := findCompose(root)
	if e != nil {
		return e
	}
	args := []string{"compose", "-f", compose, "build"}
	args = append(args, services...)
	if _, e := runIn(root, "docker", args...); e != nil {
		return fmt.Errorf("docker build basarisiz: %w", e)
	}
	args = []string{"compose", "-f", compose, "up", "-d", "--no-deps"}
	args = append(args, services...)
	if _, e := runIn(root, "docker", args...); e != nil {
		return fmt.Errorf("docker up basarisiz: %w", e)
	}
	for i := 0; i < 6; i++ {
		ok := true
		for _, s := range services {
			out, e := runIn(root, "docker", "compose", "-f", compose, "ps", "-q", s)
			if e != nil || strings.TrimSpace(string(out)) == "" {
				ok = false
				break
			}
			id := strings.Fields(string(out))[0]
			state, e := runIn(root, "docker", "inspect", "--format", "{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}", id)
			if e != nil || (strings.TrimSpace(string(state)) != "healthy" && strings.TrimSpace(string(state)) != "running") {
				ok = false
				break
			}
		}
		if ok {
			return nil
		}
		time.Sleep(5 * time.Second)
	}
	return errors.New("servisler running/healthy degil")
}
func runIn(dir, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, e := cmd.CombinedOutput()
	if e != nil {
		return out, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), e, strings.TrimSpace(string(out)))
	}
	return out, nil
}