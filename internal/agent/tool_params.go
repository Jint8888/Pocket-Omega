package agent

// baseToolKeyParams maps tool names to their primary semantic parameter key.
// Shared by loop detection (paramDedupTools) and auto-summary (autoSummaryParamKeys)
// to extract the meaningful parameter for deduplication and display.
//
// ⚠️ When adding new tools with a clear "key parameter", update this map
// so both loop detection and walkthrough auto-summary benefit automatically.
var baseToolKeyParams = map[string]string{
	"file_read":   "path",
	"file_write":  "path",
	"file_patch":  "path",
	"file_list":   "path",
	"file_move":   "path",
	"file_delete": "path",
	"file_grep":   "path",
	"shell_exec":  "command",
	"config_edit": "key",
}

// mergeToolKeyParams creates a new map from baseToolKeyParams + extras.
func mergeToolKeyParams(extras map[string]string) map[string]string {
	m := make(map[string]string, len(baseToolKeyParams)+len(extras))
	for k, v := range baseToolKeyParams {
		m[k] = v
	}
	for k, v := range extras {
		m[k] = v
	}
	return m
}
