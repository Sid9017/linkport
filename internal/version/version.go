package version

import "fmt"

var (
	Version = "0.0.5-dev"
	Channel = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

func String() string {
	return fmt.Sprintf("%s [%s] (commit %s, built %s)", Version, Channel, Commit, Date)
}
