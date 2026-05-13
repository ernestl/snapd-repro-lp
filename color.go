package main

// ANSI escape codes for terminal colour output.
const (
	colorReset  = "\033[0m"
	colorBold   = "\033[1m"
	colorDim    = "\033[2m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorCyan   = "\033[36m"
)

func bold(s string) string     { return colorBold + s + colorReset }
func dim(s string) string      { return colorDim + s + colorReset }
func green(s string) string    { return colorGreen + s + colorReset }
func blue(s string) string     { return colorBlue + s + colorReset }
func red(s string) string      { return colorRed + s + colorReset }
func yellow(s string) string   { return colorYellow + s + colorReset }
func cyan(s string) string     { return colorCyan + s + colorReset }
func boldCyan(s string) string { return colorBold + colorCyan + s + colorReset }
