package render

import (
	"fmt"
	"os"
	"time"

	"github.com/briandowns/spinner"
	"github.com/fatih/color"
	"golang.org/x/term"

	"multi-wallet-cli/cli/theme"
)

type Spinner struct {
	label string
	spin  *spinner.Spinner
}

func StartSpinner(label string) *Spinner {
	if os.Getenv("NO_COLOR") != "" || !term.IsTerminal(int(os.Stdout.Fd())) {
		fmt.Println(theme.Pending(label))
		return &Spinner{label: label}
	}
	s := spinner.New(spinner.CharSets[14], 100*time.Millisecond)
	s.Suffix = " " + label
	s.Color("yellow")
	s.Start()
	return &Spinner{label: label, spin: s}
}

func (s *Spinner) Success(message string) {
	if s == nil {
		return
	}
	if s.spin != nil {
		s.spin.Stop()
	}
	fmt.Println(theme.Success(message))
}

func (s *Spinner) Error(message string) {
	if s == nil {
		return
	}
	if s.spin != nil {
		s.spin.Stop()
	}
	fmt.Println(theme.Error(message))
}

func (s *Spinner) Stop() {
	if s == nil || s.spin == nil {
		return
	}
	s.spin.Stop()
}

func init() {
	color.NoColor = os.Getenv("NO_COLOR") != ""
}
