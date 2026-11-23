package notifications

import (
  "math"
  "strings"
  "testing"
  "time"
)
func ptr(v int64)*int64{return &v}
func TestQuotaThresholdsAndNoInventedMeasurements(t *testing.T){
 now:=time.Date(2026,11,23,10,0,0,0,time.UTC)
 base:=Snapshot{Organization:"tenant-a",Period:"2026-11",SitesLimit:ptr(100)}
 n,e:=Evaluate(base,now);if e!=nil||len(n)!=0{t.Fatalf("unknown data: %v %v",n,e)}
 for _,test:=range []struct{used int64;expected int;level Severity}{
  {69,0,""},{70,1,Info},{89,1,Info},{90,1,Warning},{99,1,Warning},{100,1,Critical},{101,1,Critical},
 }{
  s:=base;s.SitesUsed=ptr(test.used)
  notices,err:=Evaluate(s,now)
  if err!=nil || len(notices)!=test.expected{t.Fatalf("%d: %v %v",test.used,notices,err)}
  if len(notices)>0 && notices[0].Severity!=test.level{t.Fatalf("%d level %s",test.used,notices[0].Severity)}
 }
 s:=base;s.SitesUsed=ptr(math.MaxInt64);s.SitesLimit=ptr(math.MaxInt64)
 n,e=Evaluate(s,now)
 if e!=nil||len(n)!=1||n[0].Severity!=Critical{t.Fatalf("overflow: %v %v",n,e)}
 s.SitesUsed=ptr(-1)
 if _,e:=Evaluate(s,now);e==nil{t.Fatal("negative counter accepted")}
 s.SitesUsed=ptr(4);s.Period=""
 if _,e:=Evaluate(s,now);e==nil{t.Fatal("missing usage period accepted")}
}
func TestSSLDatesAndExplicitOriginObservation(t *testing.T){
 now:=time.Date(2026,11,23,10,0,0,0,time.UTC)
 s:=Snapshot{Organization:"tenant-a",Certificates:[]Certificate{
  {Domain:"valid.example",ExpiresAt:now.Add(60*24*time.Hour),Observed:true},
  {Domain:"soon.example",ExpiresAt:now.Add(5*24*time.Hour),Observed:true},
  {Domain:"expired.example",ExpiresAt:now.Add(-time.Hour),Observed:true},
  {Domain:"unknown.example",Observed:false},
 },Origins:[]Origin{
  {Name:"origin-a",Observed:false,Healthy:false},
  {Name:"origin-b",Observed:true,Healthy:true},
  {Name:"origin-c",Observed:true,Healthy:false},
 }}
 n,e:=Evaluate(s,now)
 if e!=nil||len(n)!=3 {t.Fatalf("alerts: %v %v",n,e)}
 levels:=map[string]Severity{}
 for _,alert:=range n{levels[alert.Resource]=alert.Severity}
 if levels["soon.example"]!=Warning||levels["expired.example"]!=Critical||levels["origin-c"]!=Warning{
  t.Fatalf("wrong severity: %v",levels)
 }
 if levels["unknown.example"]!=""||levels["origin-a"]!=""{t.Fatal("unobserved state generated an alert")}
}
func TestStableKeysAndTenantIsolation(t *testing.T){
 now:=time.Date(2026,11,23,10,0,0,0,time.UTC)
 s:=Snapshot{Organization:"tenant-a",Period:"2026-11",RequestsUsed:ptr(90),RequestsLimit:ptr(100)}
 a,e:=Evaluate(s,now);if e!=nil||len(a)!=1{t.Fatal(e)}
 b,e:=Evaluate(s,now.Add(30*time.Second));if e!=nil||len(b)!=1{t.Fatal(e)}
 if a[0].Key!=b[0].Key{t.Fatal("dedupe ID unstable")}
 s.Organization="tenant-b"
 c,e:=Evaluate(s,now);if e!=nil||len(c)!=1{t.Fatal(e)}
 if a[0].Key==c[0].Key{t.Fatal("tenant IDs collided")}
 combined:=append(a,c...)
 if got:=FilterByOrganization(combined,"tenant-a");len(got)!=1||got[0].Organization!="tenant-a"{
  t.Fatalf("cross tenant leak: %+v",got)
 }
 if len(FilterByOrganization(combined,""))!=0{t.Fatal("empty tenant authorized")}
 if !strings.Contains(a[0].Detail,"Hizmet otomatik kapatılmayacak"){
  t.Fatal("must explicitly avoid automatic disabling")
 }
}
