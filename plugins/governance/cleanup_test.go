package governance

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func requirePluginCleanup(t *testing.T, plugin *GovernancePlugin) {
	t.Helper()
	require.NoError(t, plugin.Cleanup())
}

func requireTrackerCleanup(t *testing.T, tracker *UsageTracker) {
	t.Helper()
	require.NoError(t, tracker.Cleanup())
}
