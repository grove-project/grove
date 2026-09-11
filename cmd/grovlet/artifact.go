package main

import "github.com/grove-project/grove/internal/artifact"

const groveShopArtifactManifest = `{"format_version":1,"application_id":"grove-shop","code_version":"v0.1.0-dev","components":[{"service_id":1,"name":"Orders","runtime":"process","entrypoint":["worker","--component","orders"]},{"service_id":2,"name":"Inventory","runtime":"process","entrypoint":["worker","--component","inventory"]},{"service_id":5,"name":"Web","runtime":"process","entrypoint":["worker","--component","web"]}],"ui_assets":["web/index.html"],"config_region":{"format_version":1,"capacity":4096}}`

const (
	blankConfig16   = "\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00"
	blankConfig256  = blankConfig16 + blankConfig16 + blankConfig16 + blankConfig16 + blankConfig16 + blankConfig16 + blankConfig16 + blankConfig16 + blankConfig16 + blankConfig16 + blankConfig16 + blankConfig16 + blankConfig16 + blankConfig16 + blankConfig16 + blankConfig16
	blankConfig4096 = blankConfig256 + blankConfig256 + blankConfig256 + blankConfig256 + blankConfig256 + blankConfig256 + blankConfig256 + blankConfig256 + blankConfig256 + blankConfig256 + blankConfig256 + blankConfig256 + blankConfig256 + blankConfig256 + blankConfig256 + blankConfig256

	embeddedGroveShopArtifact = artifact.ManifestPrefix + groveShopArtifactManifest + artifact.ManifestSuffix +
		artifact.ConfigRegionPrefix + blankConfig4096 + artifact.ConfigRegionSuffix
)

func inspectEmbeddedGroveShopArtifact() (artifact.Inspection, error) {
	return artifact.InspectBytes([]byte(embeddedGroveShopArtifact))
}
