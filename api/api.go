// Package api embeds the frozen execution contracts so the server, tests and
// tooling read exactly the files published in this repository.
package api

import "embed"

// FS holds the OpenAPI document and the machine-readable contract manifests.
//
//go:embed openapi/contracts.v1.yaml contracts/*.yaml
var FS embed.FS

const (
	OpenAPIPath     = "openapi/contracts.v1.yaml"
	RolesPath       = "contracts/roles.v1.yaml"
	CloudEventsPath = "contracts/cloudevents.v1.yaml"
	MicePath        = "contracts/mice.v1.yaml"
	SpeciesPath     = "contracts/species.v1.yaml"
	AnalyticsPath   = "contracts/analytics.v1.yaml"
)
