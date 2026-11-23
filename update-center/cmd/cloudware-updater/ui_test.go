package main

import (
    "bytes"
    "fmt"
    "io"
    "net/http"
    "net/http/httptest"
    "strings"
    "sync"
    "testing"
    "time"
)

func uiRequest(app *uiApp, method, path, token, origin, body string) *httptest.ResponseRecorder {
    request:=httptest.NewRequest(method,"http://"+app.address+path,strings.NewReader(body))
    request.Host=app.address
    if token!="" {request.Header.Set("X-Cloudware-Token",token)}
    if origin!="" {request.Header.Set("Origin",origin)}
    request.Header.Set("Content-Type","application/json")
    rec:=httptest.NewRecorder()
    app.handler().ServeHTTP(rec,request)
    return rec
}
func waitUI(t *testing.T, app *uiApp) uiState {
    t.Helper()
    deadline:=time.Now().Add(5*time.Second)
    for time.Now().Before(deadline){
        app.mu.Lock(); s:=app.state; app.mu.Unlock()
        if !s.Busy {return s}
        time.Sleep(15*time.Millisecond)
    }
    t.Fatal("UI action did not finish in timeout")
    return uiState{}
}
func TestUIAuthenticationAndCSRF(t *testing.T){
    app,err:=newUI(t.TempDir());if err!=nil {t.Fatal(err)}
    app.address="127.0.0.1:39231"
    if rec:=uiRequest(app,"GET","/","","",""); rec.Code!=200 || !strings.Contains(rec.Body.String(),"Cloudware Update Center"){
        t.Fatalf("HTML failed %d: %s",rec.Code,rec.Body.String())
    }
    if rec:=uiRequest(app,"GET","/assets/ui.js","","",""); rec.Code!=200 || !strings.Contains(rec.Body.String(),"addEventListener"){
        t.Fatal("button JS not served")
    }
    if rec:=uiRequest(app,"GET","/api/state","","","");rec.Code!=403 {t.Fatal("unauthenticated state accepted")}
    if rec:=uiRequest(app,"GET","/api/state",app.token,"","");rec.Code!=200 {t.Fatal("authenticated state failed")}
    if rec:=uiRequest(app,"POST","/api/action",app.token,"https://evil.example",`{"action":"check"}`);rec.Code!=403{t.Fatal("cross-origin action accepted")}
    if rec:=uiRequest(app,"POST","/api/action","","http://"+app.address,`{"action":"check"}`);rec.Code!=403{t.Fatal("missing token accepted")}
    if rec:=uiRequest(app,"POST","/api/action",app.token,"http://"+app.address,`{"action":"drop-db"}`);rec.Code!=400{t.Fatal("unknown action accepted")}
    if rec:=uiRequest(app,"POST","/api/action",app.token,"http://"+app.address,`{"action":"install","approval":"KUR cw-app-0.5.7"}`);rec.Code!=409{t.Fatal("install without verified download accepted")}
    rec:=uiRequest(app,"GET","/","","","")
    if !strings.Contains(rec.Header().Get("Content-Security-Policy"),"script-src 'self'") {t.Fatal("CSP missing")}
    request:=httptest.NewRequest("GET","http://malicious.example/api/state",nil)
    request.Header.Set("X-Cloudware-Token",app.token)
    spoof:=httptest.NewRecorder();app.handler().ServeHTTP(spoof,request)
    if spoof.Code!=403{t.Fatal("DNS rebinding Host accepted")}
}

func TestUIRealApprovalWorkflow(t *testing.T){
    app,err:=newUI(t.TempDir());if err!=nil{t.Fatal(err)}
    app.address="127.0.0.1:39232"
    var mu sync.Mutex
    invoked:=[]string{}
    app.runner=func(action, root,approval string,w io.Writer)error{
        mu.Lock();invoked=append(invoked,action+":"+approval);mu.Unlock()
        switch action{
        case "check": io.WriteString(w,"[NEW] 2 bekleyen surum, hedef cw-app-0.5.7\n")
        case "download": io.WriteString(w,"[NEW] 2 bekleyen surum, hedef cw-app-0.5.7\n[PASS] imza dogrulandi\n[READY] Toplu paket indirildi ve dogrulandi\n")
        case "install":
            if approval!="KUR cw-app-0.5.7"{return fmt.Errorf("bad approval")}
            io.WriteString(w,"[PASS] Kurulum tamamlandi\n")
        default:return fmt.Errorf("invalid mode")
        }
        return nil
    }
    origin:="http://"+app.address
    for _,mode:=range []string{"check","download"}{
        rec:=uiRequest(app,"POST","/api/action",app.token,origin,fmt.Sprintf(`{"action":%q}`,mode))
        if rec.Code!=202 {t.Fatalf("%s HTTP %d",mode,rec.Code)}
        waitUI(t,app)
    }
    app.mu.Lock(); tag:=app.state.VerifiedTag;app.mu.Unlock()
    if tag!="cw-app-0.5.7" {t.Fatalf("wrong verified tag %q",tag)}
    rec:=uiRequest(app,"POST","/api/action",app.token,origin,`{"action":"install","approval":"KUR cw-app-0.5.6"}`)
    if rec.Code!=409{t.Fatal("wrong confirmation accepted")}
    rec=uiRequest(app,"POST","/api/action",app.token,origin,`{"action":"install","approval":"KUR cw-app-0.5.7"}`)
    if rec.Code!=202{t.Fatalf("install rejected HTTP %d",rec.Code)}
    state:=waitUI(t,app)
    if state.VerifiedTag!="" || !strings.Contains(state.Message,"[PASS]") {t.Fatalf("bad post-install state %+v",state)}
    mu.Lock();defer mu.Unlock()
    if len(invoked)!=3 || invoked[2]!="install:KUR cw-app-0.5.7" {t.Fatalf("wrong calls %v",invoked)}
}

func TestUIConcurrentActionBlocked(t *testing.T){
    app,err:=newUI(t.TempDir());if err!=nil{t.Fatal(err)}
    app.address="127.0.0.1:39233"
    release:=make(chan struct{})
    app.runner=func(_,_ ,_ string,_ io.Writer)error{<-release;return nil}
    first:=uiRequest(app,"POST","/api/action",app.token,"http://"+app.address,`{"action":"check"}`)
    if first.Code!=202{t.Fatal(first.Code)}
    second:=uiRequest(app,"POST","/api/action",app.token,"http://"+app.address,`{"action":"download"}`)
    if second.Code!=409{t.Fatal("concurrent operation accepted")}
    close(release)
    waitUI(t,app)
    // Browser/script injection must never be sourced from logs as HTML.
    var buf bytes.Buffer
    io.WriteString(&buf,"<script>alert(1)</script>")
    app.mu.Lock();app.state.Logs=buf.String();app.mu.Unlock()
    rec:=uiRequest(app,"GET","/api/state",app.token,"","")
    if rec.Code!=200 {t.Fatal(rec.Code)}
    if strings.Contains(rec.Body.String(),"<script>"){t.Fatal("JSON encoder emitted raw HTML")}
}
