package qoderproxy

// defaultModelIDs are the model keys Qoder's agent endpoint accepts in
// model_config.key. "auto" lets Qoder pick the model per request.
// Keep in sync with the "qoder" list in frontend useModelWhitelist.ts.
var defaultModelIDs = []string{
	"auto",
	"qmodel_preview", "qmodel_38max", "qmodel_latest", "qmodel",
	"kmodel_latest", "kmodel",
	"gm51model", "cmodel",
	"dmodel", "dfmodel", "mmodel",
}

// DefaultModelIDs returns the models advertised for Qoder groups when no
// account restricts or maps models.
func DefaultModelIDs() []string {
	out := make([]string, len(defaultModelIDs))
	copy(out, defaultModelIDs)
	return out
}
