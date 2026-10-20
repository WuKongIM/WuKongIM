package main

import (
 "encoding/json"
 "fmt"
 "os"
 "github.com/google/pprof/profile"
)

type Result struct {
 File string `json:"file"`
 Type string `json:"type"`
 Unit string `json:"unit"`
 TimeNS int64 `json:"time_ns"`
 DurationNS int64 `json:"duration_ns"`
 Period int64 `json:"period"`
 Total int64 `json:"total"`
 Flat map[string]int64 `json:"flat"`
 Cumulative map[string]int64 `json:"cumulative"`
 Labels map[string]map[string]int64 `json:"labels"`
}
func main() {
 var results []Result
 for _, file := range os.Args[1:] {
  f, err := os.Open(file); if err != nil { panic(err) }
  p, err := profile.Parse(f); f.Close(); if err != nil { panic(err) }
  index := -1
  for i, t := range p.SampleType { if t.Type == "cpu" || t.Type == "alloc_space" { index=i; break } }
  if index<0 { panic(fmt.Errorf("unsupported sample type: %s", file)) }
  r:= Result{File:file, Type:p.SampleType[index].Type, Unit:p.SampleType[index].Unit, TimeNS:p.TimeNanos, DurationNS:p.DurationNanos, Period:p.Period, Flat:map[string]int64{}, Cumulative:map[string]int64{}, Labels:map[string]map[string]int64{}}
  for _, s := range p.Sample {
   value:=s.Value[index]; r.Total+=value
   seen:=map[string]bool{}
   first:=true
   for _, l:=range s.Location { for _, line:=range l.Line {
    if line.Function==nil { continue }
    name:=line.Function.Name
    if first { r.Flat[name]+=value; first=false }
    if !seen[name] { r.Cumulative[name]+=value; seen[name]=true }
   } }
   for k, vs:=range s.Label { if r.Labels[k]==nil { r.Labels[k]=map[string]int64{} }; for _, v:=range vs { r.Labels[k][v]+=value } }
  }
  results=append(results,r)
 }
 if err:=json.NewEncoder(os.Stdout).Encode(results);err!=nil {panic(err)}
}
