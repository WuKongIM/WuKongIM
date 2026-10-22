package main
import("crypto/sha256";"encoding/hex";"encoding/json";"fmt";"os"; chat "github.com/WuKongIM/WuKongIM/internal/bench/chatlifecycle")
type Planned struct {Config chat.Config `json:"config"`;RunHash string `json:"run_hash"`;AssignmentHash string `json:"assignment_hash"`;AssignmentCount int `json:"assignment_count"`;Generation uint64 `json:"generation"`}
func plan(path string)(Planned,error){cfg,err:=chat.LoadConfig(path);if err!=nil{return Planned{},err};assignments,err:=chat.BuildCoordinatorAssignments(cfg,1);if err!=nil{return Planned{},err};if len(assignments)!=3{return Planned{},fmt.Errorf("wrong assignment count")};f:=assignments[0].WorkerAssignment.WorkerFence;for _,a:=range assignments{if a.WorkerAssignment.WorkerFence!=f{return Planned{},fmt.Errorf("assignment fence mismatch")}};return Planned{Config:cfg,RunHash:hashReportValue(f.RunID),AssignmentHash:hashReportValue(f.AssignmentID),AssignmentCount:len(assignments),Generation:f.Generation},nil}
func hashReportValue(value string) string {
	digest := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(digest[:])
}
func main(){if len(os.Args)!=3{fmt.Fprintln(os.Stderr,"two config paths required");os.Exit(2)};a,err:=plan(os.Args[1]);if err!=nil{fmt.Fprintln(os.Stderr,err);os.Exit(1)};b,err:=plan(os.Args[2]);if err!=nil{fmt.Fprintln(os.Stderr,err);os.Exit(1)};if err=json.NewEncoder(os.Stdout).Encode(map[string]any{"schema":"wukongim/offline-existing-loader-planner/v1","product_started":false,"baseline":a,"candidate":b});err!=nil{os.Exit(1)}}
