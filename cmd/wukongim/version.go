package main

var (
	buildVersion = "dev"
	buildCommit  = "unknown"
	buildSource  = "source"
)

type buildInfo struct {
	Version     string `json:"version"`
	Commit      string `json:"commit"`
	BuildSource string `json:"build_source"`
}

func currentBuildInfo() buildInfo {
	return buildInfo{
		Version:     buildVersion,
		Commit:      buildCommit,
		BuildSource: buildSource,
	}
}
