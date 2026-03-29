package cmd

import (
	"fmt"
	"strings"

	"github.com/muesli/termenv"
)

type summaryMetric struct {
	Label string
	Value int
}

func metric(label string, value int) summaryMetric {
	return summaryMetric{Label: label, Value: value}
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

type cmdOutput struct {
	info    termenv.Style
	success termenv.Style
	warn    termenv.Style
	error   termenv.Style
	tip     termenv.Style
}

func newCmdOutput() cmdOutput {
	p := termenv.ColorProfile()
	return cmdOutput{
		info:    termenv.String().Foreground(p.Color("14")).Bold(),
		success: termenv.String().Foreground(p.Color("10")).Bold(),
		warn:    termenv.String().Foreground(p.Color("11")).Bold(),
		error:   termenv.String().Foreground(p.Color("9")).Bold(),
		tip:     termenv.String().Foreground(p.Color("14")).Bold(),
	}
}

func (o cmdOutput) Info(message string) {
	fmt.Println(o.info.Styled("ℹ️  " + message))
}

func (o cmdOutput) Success(message string) {
	fmt.Println(o.success.Styled("✅ " + message))
}

func (o cmdOutput) Warn(message string) {
	fmt.Println(o.warn.Styled("⚠️  " + message))
}

func (o cmdOutput) Error(message string) {
	fmt.Println(o.error.Styled("❌ " + message))
}

func (o cmdOutput) Tip(message string) {
	fmt.Println(o.tip.Styled("💡 " + message))
}

func (o cmdOutput) Blank() {
	fmt.Println()
}

func (o cmdOutput) Summary(title string, metrics ...summaryMetric) {
	o.Info(fmt.Sprintf("%s summary:", title))
	for _, m := range metrics {
		fmt.Printf("- %s: %d\n", humanizeMetricLabel(m.Label), m.Value)
	}
}

func humanizeMetricLabel(label string) string {
	words := strings.Fields(strings.ReplaceAll(label, "_", " "))
	for i := range words {
		if words[i] == "" {
			continue
		}
		words[i] = strings.ToUpper(words[i][:1]) + words[i][1:]
	}
	return strings.Join(words, " ")
}
