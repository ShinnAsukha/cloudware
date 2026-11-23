// Package notifications calculates organization-scoped advisories from verified snapshots.
// It never reads a database, changes a plan, disables a website or sends mail.
package notifications

import (
    "crypto/sha256"
    "fmt"
    "math/big"
    "regexp"
    "sort"
    "strings"
    "time"
)
type Severity string
const (
    Info Severity="info"
    Warning Severity="warning"
    Critical Severity="critical"
)
type Notice struct {
    Key string `json:"key"`
    Organization string `json:"organization"`
    Kind string `json:"kind"`
    Severity Severity `json:"severity"`
    Title string `json:"title"`
    Detail string `json:"detail"`
    Source string `json:"source"`
    Resource string `json:"resource"`
    EvaluatedAt time.Time `json:"evaluated_at"`
}
type Certificate struct {
    Domain string
    ExpiresAt time.Time
    Observed bool // A missing/unverified expiry date must not generate an alert.
}
type Origin struct {
    Name string
    Observed bool // Explicitly observed health state, not a default false.
    Healthy bool
}
type Snapshot struct {
    Organization string
    Period string // YYYY-MM for usage metrics; required when usage is supplied
    SitesUsed *int64
    SitesLimit *int64
    RequestsUsed *int64
    RequestsLimit *int64
    Certificates []Certificate
    Origins []Origin
}
var periodRE=regexp.MustCompile(`^[0-9]{4}-(0[1-9]|1[0-2])$`)

func identity(parts ...string) string {
    h:=sha256.Sum256([]byte(strings.Join(parts,"\x00")))
    return fmt.Sprintf("cw-v1-%x",h[:12])
}
func compareThreshold(used, limit int64, percent int64) bool {
    if limit==0 { return used>0 }
    a:=new(big.Int).Mul(big.NewInt(used),big.NewInt(100))
    b:=new(big.Int).Mul(big.NewInt(limit),big.NewInt(percent))
    return a.Cmp(b)>=0
}
func quota(org,period,resource,label string,used,limit *int64,now time.Time) (*Notice,error){
    if used==nil || limit==nil{return nil,nil}
    if *used<0 || *limit<0{return nil,fmt.Errorf("negative %s metric",resource)}
    if !periodRE.MatchString(period){return nil,fmt.Errorf("usage period required for %s",resource)}
    level:=int64(0); sev:=Info
    if compareThreshold(*used,*limit,100){level=100;sev=Critical
    }else if compareThreshold(*used,*limit,90){level=90;sev=Warning
    }else if compareThreshold(*used,*limit,70){level=70;sev=Info
    }else{return nil,nil}
    n:=Notice{
        Key:identity(org,resource,period,fmt.Sprint(level)),
        Organization:org,Kind:"quota",Severity:sev,
        Title:label+" kullanım uyarısı",
        Detail:fmt.Sprintf("%s: %d / %d (eşik %d%%). Hizmet otomatik kapatılmayacak.",label,*used,*limit,level),
        Source:"usage",Resource:resource,EvaluatedAt:now,
    }
    return &n,nil
}
func Evaluate(snapshot Snapshot,now time.Time)([]Notice,error){
    if strings.TrimSpace(snapshot.Organization)=="" || len(snapshot.Organization)>200 {
        return nil,fmt.Errorf("organization required")
    }
    now=now.UTC()
    out:=[]Notice{}
    for _, metric :=range []struct{
        name,label string
        used,limit *int64
    }{
        {"sites","Site",snapshot.SitesUsed,snapshot.SitesLimit},
        {"monthly_requests","Aylık HTTP isteği",snapshot.RequestsUsed,snapshot.RequestsLimit},
    } {
        n,err:=quota(snapshot.Organization,snapshot.Period,metric.name,metric.label,metric.used,metric.limit,now)
        if err!=nil{return nil,err}
        if n!=nil{out=append(out,*n)}
    }
    for _,cert:=range snapshot.Certificates{
        if !cert.Observed {continue}
        if cert.Domain=="" || len(cert.Domain)>253 || cert.ExpiresAt.IsZero(){
            return nil,fmt.Errorf("invalid observed certificate")
        }
        remaining:=cert.ExpiresAt.UTC().Sub(now)
        var sev Severity
        var level string
        switch {
        case remaining<=0: sev=Critical;level="expired"
        case remaining<=7*24*time.Hour: sev=Warning;level="7d"
        case remaining<=30*24*time.Hour:sev=Info;level="30d"
        default:continue
        }
        out=append(out,Notice{
            Key:identity(snapshot.Organization,"certificate",cert.Domain,cert.ExpiresAt.UTC().Format("2006-01-02"),level),
            Organization:snapshot.Organization,Kind:"ssl",Severity:sev,
            Title:"SSL sertifika durumu",
            Detail:fmt.Sprintf("%s: sertifika için %s uyarısı. Otomatik yenileme sonucu ayrıca doğrulanmalı.",cert.Domain,level),
            Source:"tls",Resource:cert.Domain,EvaluatedAt:now,
        })
    }
    for _,origin:=range snapshot.Origins{
        if !origin.Observed || origin.Healthy {continue}
        if strings.TrimSpace(origin.Name)=="" || len(origin.Name)>253 {return nil,fmt.Errorf("invalid origin")}
        out=append(out,Notice{
            Key:identity(snapshot.Organization,"origin",origin.Name,now.Format("2006-01-02")),
            Organization:snapshot.Organization,Kind:"origin",Severity:Warning,
            Title:"Origin bağlantısı kontrol edilmeli",
            Detail:fmt.Sprintf("%s için erişilebilirlik kontrolü başarısız. Tek ölçümden kalıcı kesinti sonucu çıkarılmamalı.",origin.Name),
            Source:"origin-health",Resource:origin.Name,EvaluatedAt:now,
        })
    }
    sort.Slice(out,func(i,j int)bool{return out[i].Key<out[j].Key})
    return out,nil
}

// FilterByOrganization is a second defense; it is NOT an authorization check.
// The caller must first authenticate and authorize the requested organization.
func FilterByOrganization(all []Notice, authorizedOrganization string) []Notice {
    out:=[]Notice{}
    for _,n:=range all{
        if authorizedOrganization!="" && n.Organization==authorizedOrganization{out=append(out,n)}
    }
    return out
}
