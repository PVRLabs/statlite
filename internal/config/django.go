package config

const (
	DjangoMetricsModeInline   = "inline"
	DjangoMetricsModeSeparate = "separate"
)

func ValidDjangoMetricsMode(mode string) bool {
	return mode == DjangoMetricsModeInline || mode == DjangoMetricsModeSeparate
}
