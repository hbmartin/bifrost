package handlers

import (
	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
)

var version string
var logger schemas.Logger = bifrost.NewNoOpLogger()

// SetLogger sets the logger for the application.
func SetLogger(l schemas.Logger) {
	if l == nil {
		logger = bifrost.NewNoOpLogger()
		return
	}
	logger = l
}

// SetVersion sets the version for the application.
func SetVersion(v string) {
	version = v
}

func GetVersion() string {
	return version
}
