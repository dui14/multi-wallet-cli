package theme

import (
	"os"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/fatih/color"
)

var (
	accent  = color.New(color.FgHiMagenta, color.Bold)
	success = color.New(color.FgGreen)
	warning = color.New(color.FgYellow)
	danger  = color.New(color.FgHiRed)
	info    = color.New(color.FgHiBlue)
	pending = color.New(color.FgYellow)
	fg      = color.New(color.FgWhite)
	muted   = color.New(color.FgHiBlack)
	subtle  = color.New(color.FgHiBlack, color.Faint)
	value   = color.New(color.FgWhite, color.Bold)
)

func Init() {
	if os.Getenv("NO_COLOR") != "" {
		color.NoColor = true
	}
}

func Title(s string) string {
	return accent.Sprint(strings.ToUpper(s))
}

func Body(s string) string {
	return fg.Sprint(s)
}

func Label(s string) string {
	return muted.Sprint(s)
}

func Value(s string) string {
	return value.Sprint(s)
}

func Help(s string) string {
	return subtle.Sprint(s)
}

func Code(s string) string {
	return info.Sprint(s)
}

func Success(s string) string {
	return success.Sprintf("%s %s", GlyphSuccess(), s)
}

func Warning(s string) string {
	return warning.Sprintf("%s %s", GlyphWarning(), s)
}

func Error(s string) string {
	return danger.Sprintf("%s %s", GlyphError(), s)
}

// Ok renders a success-colored cell without a glyph prefix, for table cells
// where the glyph would add noise.
func Ok(s string) string {
	return success.Sprint(s)
}

// Fail renders a danger-colored cell without a glyph prefix, for table cells.
func Fail(s string) string {
	return danger.Sprint(s)
}

// Dash renders the not-applicable placeholder used when a token does not exist
// on a chain, keeping the caller-supplied glyph from the i18n bundle.
func Dash(s string) string {
	return muted.Sprint(s)
}

func Info(s string) string {
	return info.Sprintf("%s %s", GlyphInfo(), s)
}

func Pending(s string) string {
	return pending.Sprintf("%s %s", GlyphPending(), s)
}

func GlyphSuccess() string {
	return "✔"
}

func GlyphError() string {
	return "✖"
}

func GlyphWarning() string {
	return "⚠"
}

func GlyphInfo() string {
	return "ℹ"
}

func GlyphPending() string {
	return "●"
}

func SurveyOpts() []survey.AskOpt {
	return []survey.AskOpt{
		survey.WithIcons(func(icons *survey.IconSet) {
			icons.SelectFocus.Text = "❯"
			icons.SelectFocus.Format = "cyan"
			icons.Question.Format = "cyan"
			icons.MarkedOption.Format = "green"
		}),
	}
}

func TruncateMiddle(value string, max int) string {
	if max <= 0 || len(value) <= max {
		return value
	}
	if max < 8 {
		return value[:max]
	}
	left := (max - 1) / 2
	right := max - 1 - left
	return value[:left] + "…" + value[len(value)-right:]
}
