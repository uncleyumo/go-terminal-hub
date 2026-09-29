package buildinfo

type BuildInfo struct {
	Version   string `json:"version"`
	BuildTime string `json:"buildTime"`
}

var version string
var buildTime string

func Info() BuildInfo {
	return BuildInfo{
		Version:   version,
		BuildTime: buildTime,
	}
}
