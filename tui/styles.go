package tui

import "github.com/charmbracelet/lipgloss"

var (
	accent   = lipgloss.AdaptiveColor{Light: "#005fd7", Dark: "#5fafff"}
	selected = lipgloss.AdaptiveColor{Light: "#008700", Dark: "#87d75f"}
	cursorC  = lipgloss.AdaptiveColor{Light: "#d75f00", Dark: "#ffaf00"}
	subtle   = lipgloss.AdaptiveColor{Light: "#8a8a8a", Dark: "#6c6c6c"}
	errorC   = lipgloss.AdaptiveColor{Light: "#d70000", Dark: "#ff5f5f"}

	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(accent)
	subtleStyle   = lipgloss.NewStyle().Foreground(subtle)
	errorStyle    = lipgloss.NewStyle().Foreground(errorC)
	okStyle       = lipgloss.NewStyle().Foreground(selected)
	cursorStyle   = lipgloss.NewStyle().Foreground(cursorC).Bold(true)
	selectedStyle = lipgloss.NewStyle().Foreground(selected)
	activeTab     = lipgloss.NewStyle().Bold(true).Foreground(accent).Underline(true)
	inactiveTab   = lipgloss.NewStyle().Foreground(subtle)
	helpStyle     = lipgloss.NewStyle().Foreground(subtle)
)
