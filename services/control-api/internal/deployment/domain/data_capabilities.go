package domain

import deploymentv1 "github.com/liuzengh/trpc-agent-service/api/schemas/deployment/v1"

// Share resolved component semantics rather than maintaining a second codec.
// Manifest integration follows compiler closure; pendingDataContractDiagnostics
// remains in force until that complete integration is delivered.
type ManifestMemory = deploymentv1.ManifestMemory
type ManifestArtifact = deploymentv1.ManifestArtifact
type ManifestSummary = deploymentv1.ManifestSummary

const ArtifactMetadataContract = deploymentv1.ArtifactMetadataContract
