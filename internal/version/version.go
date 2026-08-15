// Package version contains the application release metadata.
package version

var (
	// Number is the semantic version without the conventional leading "v".
	// Release image builds replace this value with an ldflag so the binary,
	// API, UI, and OCI image metadata all identify the same release.
	Number = "0.10.1"
	// Display is the version as it should appear in the Faro interface and logs.
	Display = "v" + Number
)
