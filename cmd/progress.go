package cmd

import (
	"github.com/muesli/termenv"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
)

func newStandardProgress(total int, label string, labelStyle termenv.Style) (*mpb.Progress, *mpb.Bar) {
	if total < 0 {
		total = 0
	}

	pbar := mpb.New(mpb.WithWidth(40))
	bar := pbar.New(int64(total),
		mpb.BarStyle().Lbound("╢").Filler("█").Tip("█").Padding("·").Rbound("╟"),
		mpb.PrependDecorators(
			decor.Name(labelStyle.Styled(label), decor.WC{W: 16, C: decor.DindentRight}),
			decor.CountersNoUnit(termenv.String().Bold().Styled("%d / %d")),
		),
		mpb.AppendDecorators(
			decor.Percentage(decor.WCSyncWidth),
		),
	)

	return pbar, bar
}
