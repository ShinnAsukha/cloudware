package main

import (
    "crypto/rand"
    "encoding/hex"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "net"
    "net/http"
    "os"
    "os/exec"
    "runtime"
    "strings"
    "sync"
    "time"
    _ "embed"
)

//go:embed ui.js
var uiJavaScript []byte

// The UI is loopback-only. It NEVER installs without the user's exact
// per-release confirmation. Other websites cannot call the privileged actions.
type uiState struct {
    Busy        bool   `json:"busy"`
    Action      string `json:"action"`
    Message     string `json:"message"`
    Logs        string `json:"logs"`
    VerifiedTag string `json:"verified_tag"`
    LastUpdated string `json:"last_updated"`
}
type uiRunner func(action, root, approval string, w io.Writer) error
type uiApp struct {
    root, token, address string
    mu sync.Mutex
    state uiState
    runner uiRunner
}

func newUI(root string) (*uiApp, error) {
    nonce := make([]byte, 32)
    if _, err := rand.Read(nonce); err != nil { return nil, err }
    return &uiApp{root: root, token: hex.EncodeToString(nonce),
        state: uiState{Message:"Hazir. Önce GitHub sürümlerini kontrol et.", LastUpdated:time.Now().Format(time.RFC3339)},
        runner: runUIChild}, nil
}

func runUIChild(action, root, approval string, w io.Writer) error {
    exe, err := os.Executable()
    if err != nil { return err }
    cmd := exec.Command(exe, action, root)
    // Not a shell. Only fixed, allowlisted action names reach this subprocess.
    if action == "install" { cmd.Stdin = strings.NewReader(approval+"\n") }
    cmd.Stdout = w
    cmd.Stderr = w
    return cmd.Run()
}

func (u *uiApp) handler() http.Handler {
    mux := http.NewServeMux()
    mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        if r.URL.Path != "/" || r.Method != http.MethodGet { http.NotFound(w,r); return }
        w.Header().Set("Content-Type","text/html; charset=utf-8")
        w.WriteHeader(http.StatusOK)
        io.WriteString(w,strings.ReplaceAll(updateCenterHTML,"__CW_TOKEN__",u.token))
    })
    mux.HandleFunc("/assets/ui.js",func(w http.ResponseWriter,r *http.Request) {
        if r.Method != http.MethodGet { http.Error(w,"method not allowed",405);return }
        w.Header().Set("Content-Type","text/javascript; charset=utf-8")
        w.Write(uiJavaScript)
    })
    mux.HandleFunc("/api/state",func(w http.ResponseWriter,r *http.Request) {
        if r.Method != http.MethodGet { http.Error(w,"method not allowed",405);return }
        if r.Header.Get("X-Cloudware-Token") != u.token { http.Error(w,"unauthorized",403);return }
        u.mu.Lock(); state:=u.state; u.mu.Unlock()
        w.Header().Set("Content-Type","application/json; charset=utf-8")
        json.NewEncoder(w).Encode(state)
    })
    mux.HandleFunc("/api/action",func(w http.ResponseWriter,r *http.Request) {
        if r.Method != http.MethodPost { http.Error(w,"method not allowed",405);return }
        if r.Header.Get("Origin") != "http://"+u.address || r.Header.Get("X-Cloudware-Token") != u.token {
            http.Error(w,"forbidden",403);return
        }
        r.Body=http.MaxBytesReader(w,r.Body,2048)
        var request struct{ Action string `json:"action"`; Approval string `json:"approval"` }
        decoder:=json.NewDecoder(r.Body); decoder.DisallowUnknownFields()
        if err:=decoder.Decode(&request); err!=nil {http.Error(w,"invalid request",400);return}
        if decoder.Decode(&struct{}{}) != io.EOF { http.Error(w,"extra data",400);return }
        status, msg:=u.begin(request.Action,request.Approval)
        w.Header().Set("Content-Type","application/json")
        w.WriteHeader(status)
        json.NewEncoder(w).Encode(map[string]string{"message":msg})
    })
    return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
        // Reject browser DNS-rebinding attempts and non-loopback Host values.
        if r.Host != u.address || r.RemoteAddr == "" { http.Error(w,"invalid host",403);return }
        w.Header().Set("Cache-Control","no-store")
        w.Header().Set("X-Content-Type-Options","nosniff")
        w.Header().Set("Referrer-Policy","no-referrer")
        w.Header().Set("X-Frame-Options","DENY")
        w.Header().Set("Content-Security-Policy","default-src 'none'; script-src 'self'; style-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
        mux.ServeHTTP(w,r)
    })
}

func (u *uiApp) begin(action, approval string) (int,string) {
    if action!="check" && action!="download" && action!="install" { return 400,"gecersiz islem" }
    u.mu.Lock()
    defer u.mu.Unlock()
    if u.state.Busy { return 409,"baska bir islem devam ediyor" }
    if action=="install" {
        tag:=u.state.VerifiedTag
        if tag=="" || approval != "KUR "+tag {
            return 409,"Once indir/dogrula, sonra bu surum icin tam onay yaz"
        }
        u.state.VerifiedTag = ""
    }
    if action=="check" || action=="download" { u.state.VerifiedTag = "" }
    u.state.Busy=true
    u.state.Action=action
    u.state.Message="Islem basladi"
    u.state.Logs=""
    u.state.LastUpdated=time.Now().Format(time.RFC3339)
    go u.run(action,approval)
    return 202,"islem baslatildi"
}

type uiWriter struct { app *uiApp }
func (w uiWriter) Write(p []byte) (int,error) {
    w.app.mu.Lock()
    w.app.state.Logs += string(p)
    if len(w.app.state.Logs)>48000 {
        w.app.state.Logs=w.app.state.Logs[len(w.app.state.Logs)-48000:]
    }
    w.app.state.LastUpdated=time.Now().Format(time.RFC3339)
    w.app.mu.Unlock()
    return len(p),nil
}
func (u *uiApp) run(action, approval string) {
    err:=u.runner(action,u.root,approval,uiWriter{u})
    u.mu.Lock()
    defer u.mu.Unlock()
    u.state.Busy=false
    if err!=nil {
        u.state.Message="[FAIL] "+err.Error()
        if len(u.state.Message)>500 { u.state.Message=u.state.Message[:500] }
    } else {
        u.state.Message="[PASS] "+action+" tamamlandi"
        if action=="download" && strings.Contains(u.state.Logs,"[READY]") {
            for _, word := range strings.Fields(u.state.Logs) {
                if tagRx.MatchString(word) { u.state.VerifiedTag=word }
            }
        }
    }
    u.state.LastUpdated=time.Now().Format(time.RFC3339)
}

func startUI(root string) error {
    app,err:=newUI(root)
    if err!=nil { return err }
    listener,err:=net.Listen("tcp","127.0.0.1:0")
    if err!=nil {return err}
    defer listener.Close()
    app.address=listener.Addr().String()
    url:="http://"+app.address+"/"
    fmt.Println("Cloudware Update Center | "+url)
    fmt.Println("Yalnizca bu bilgisayarda calisir. Kurulum icin her zaman acik onay gerekir.")
    if runtime.GOOS=="windows" {
        if err:=exec.Command("rundll32.exe","url.dll,FileProtocolHandler",url).Start();err!=nil {
            fmt.Println("Tarayicinizda adresi acin:",url)
        }
    }
    server:=&http.Server{
        Handler:app.handler(),ReadHeaderTimeout:5*time.Second,
        ReadTimeout:15*time.Second,IdleTimeout:40*time.Second,MaxHeaderBytes:16<<10,
    }
    if err:=server.Serve(listener);err!=nil && !errors.Is(err,http.ErrServerClosed) {
        return err
    }
    return nil
}

const updateCenterHTML = `<!doctype html>
<html lang="tr"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="cw-token" content="__CW_TOKEN__"><title>Cloudware Update Center</title>
<style>
:root{color-scheme:dark;font-family:system-ui,-apple-system,Segoe UI,sans-serif;background:#0b1422;color:#edf3fc}
*{box-sizing:border-box}body{margin:0}main{max-width:950px;margin:48px auto;padding:0 22px}
header{display:flex;align-items:center;justify-content:space-between;gap:18px;margin-bottom:28px}
h1{font-size:28px;margin:0 0 6px}h2{font-size:18px;margin:0 0 12px}
small,.muted{color:#a2b5cc}small{font-size:12px}.mark{letter-spacing:2px;color:#fd9351;font-size:12px;font-weight:800}
.grid{display:grid;grid-template-columns:1fr 1fr;gap:18px}.card{padding:22px;background:#142238;border:1px solid #2c4661;border-radius:16px;margin:0 0 18px}
p{line-height:1.65}.tag{border:1px solid #31556e;color:#9fe2b9;padding:7px 12px;border-radius:50px;font-size:12px}
button{background:#263f5d;border:1px solid #3b5b75;color:#eef5ff;border-radius:9px;padding:12px 16px;font-weight:650;cursor:pointer;margin:4px 6px 4px 0}
button:hover{background:#365876}button:disabled{opacity:.43;cursor:not-allowed}.primary{background:#f68a45;border-color:#f68a45;color:#15202c}
input{display:block;width:100%;padding:12px;margin:12px 0;border-radius:9px;background:#091525;color:#fff;border:1px solid #4c6989}
pre{background:#070f1e;min-height:175px;max-height:400px;overflow:auto;color:#b1d4fd;padding:16px;border-radius:10px;font-size:12px;white-space:pre-wrap;word-break:break-word}
#message{margin:16px 0;padding:12px;background:#102b39;border-radius:9px;overflow-wrap:anywhere}
@media(max-width:740px){main{margin:22px auto}.grid{grid-template-columns:1fr}header{align-items:start;flex-direction:column}}
</style></head><body><main>
<header><div><div class="mark">CLOUDWARE / OX SYSTEMS</div><h1>GitHub Update Center</h1>
<div class="muted">Tek panel · Birikmiş sürümler · Kontrollü kurulum</div></div><span class="tag">LOCAL · ONAYLI</span></header>
<section class="card"><h2>Güncelleme süreci</h2><p class="muted">1. Private GitHub sürümlerini kontrol et. 2. Bekleyen sürümleri indir ve imzalarını doğrula. 3. Tam sürüm onayını vererek tek kurulumda uygula. Uygulama kendiliğinden kurulmaz.</p></section>
<div class="grid"><section class="card"><h2>GitHub ve sürüm kontrolü</h2><p class="muted">Depo: ShinnAsukha/cloudware (Private). GitHub CLI hesabın bir kez bağlı olmalıdır.</p><button id="check" type="button">Sürümleri kontrol et</button><button id="download" type="button">İndir ve doğrula</button><p><small>Yayın imzası veya kaynak SHA uymazsa kurulum durur.</small></p></section>
<section class="card"><h2>Kurulum onayı</h2><p class="muted">İndirilip doğrulanmış sürüm için aşağıdaki onay metnini gir. PostgreSQL/SQL migration bu sürümde yasaktır.</p>
<p id="verified">Henüz doğrulanmış güncelleme yok.</p><label for="approve" class="muted">Onay metni</label><input id="approve" autocomplete="off" spellcheck="false" placeholder="KUR cw-app-0.5.6">
<button id="install" class="primary" type="button" disabled>Onayla ve kur</button></section></div>
<section class="card"><h2>İşlem durumu ve kayıtlar</h2><div id="message" aria-live="polite">Hazır</div>
<pre id="logs" aria-live="polite">Henüz işlem başlatılmadı.</pre><p class="muted"><small>Güncelleme sırasında bu pencereyi kapatma. Kaynak farklıysa zorla yama yapılmaz.</small></p></section>
</main><script src="/assets/ui.js" defer></script></body></html>`
