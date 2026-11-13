package tui

import "github.com/charmbracelet/lipgloss"

// Palette uses a high-craft dark-friendly mineral/amber theme inspired by TokyoNight & Quarry stone.
var (
	ColorPrimary   = lipgloss.AdaptiveColor{Light: "#b45309", Dark: "#f59e0b"} // Quarry Amber Gold
	ColorSecondary = lipgloss.AdaptiveColor{Light: "#2563eb", Dark: "#7aa2f7"} // Electric Sapphire
	ColorAccent    = lipgloss.AdaptiveColor{Light: "#059669", Dark: "#9ece6a"} // Emerald Mint
	ColorCyan      = lipgloss.AdaptiveColor{Light: "#0891b2", Dark: "#7dcfff"} // Sky Cyan
	ColorMagenta   = lipgloss.AdaptiveColor{Light: "#7c3aed", Dark: "#bb9af7"} // Amethyst
	ColorDanger    = lipgloss.AdaptiveColor{Light: "#dc2626", Dark: "#f7768e"} // Coral Red
	ColorText      = lipgloss.AdaptiveColor{Light: "#1e293b", Dark: "#c0caf5"} // Crisp Slate
	ColorMuted     = lipgloss.AdaptiveColor{Light: "#64748b", Dark: "#565f89"} // Muted Stone
	ColorSubtle    = lipgloss.AdaptiveColor{Light: "#94a3b8", Dark: "#414868"} // Border Stone
	ColorSurface   = lipgloss.AdaptiveColor{Light: "#f1f5f9", Dark: "#1f2335"} // Elevated Surface
	ColorHighlight = lipgloss.AdaptiveColor{Light: "#fef3c7", Dark: "#292e42"} // Active Row Highlight
)

type Styles struct {
	AppHeader       lipgloss.Style
	BrandBadge      lipgloss.Style
	SiteBadgeActive lipgloss.Style
	SiteBadgeIdle   lipgloss.Style
	MetricChip      lipgloss.Style
	TabActive       lipgloss.Style
	TabInactive     lipgloss.Style
	PanelFocused    lipgloss.Style
	PanelBlurred    lipgloss.Style
	PanelTitle      lipgloss.Style
	RowSelected     lipgloss.Style
	RowNormal       lipgloss.Style
	RowChecked      lipgloss.Style
	TagChip         lipgloss.Style
	CategoryChip    lipgloss.Style
	StatusBar       lipgloss.Style
	KeyChip         lipgloss.Style
	DescChip        lipgloss.Style
	ModalBox        lipgloss.Style
}

func DefaultStyles() Styles {
	return Styles{
		AppHeader: lipgloss.NewStyle().
			Padding(0, 1).
			MarginBottom(0),

		BrandBadge: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#15161e")).
			Background(ColorPrimary).
			Padding(0, 1),

		SiteBadgeActive: lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorPrimary).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorPrimary).
			Padding(0, 1),

		SiteBadgeIdle: lipgloss.NewStyle().
			Foreground(ColorMuted).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorSubtle).
			Padding(0, 1),

		MetricChip: lipgloss.NewStyle().
			Foreground(ColorCyan).
			Padding(0, 1),

		TabActive: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#15161e")).
			Background(ColorSecondary).
			Padding(0, 1),

		TabInactive: lipgloss.NewStyle().
			Foreground(ColorText).
			Background(ColorSurface).
			Padding(0, 1),

		PanelFocused: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorPrimary).
			Padding(0, 1),

		PanelBlurred: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorSubtle).
			Padding(0, 1),

		PanelTitle: lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorPrimary),

		RowSelected: lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorPrimary).
			Background(ColorHighlight),

		RowNormal: lipgloss.NewStyle().
			Foreground(ColorText),

		RowChecked: lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorAccent),

		TagChip: lipgloss.NewStyle().
			Foreground(ColorMagenta),

		CategoryChip: lipgloss.NewStyle().
			Foreground(ColorCyan),

		StatusBar: lipgloss.NewStyle().
			Foreground(ColorText).
			Padding(0, 1),

		KeyChip: lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorPrimary),

		DescChip: lipgloss.NewStyle().
			Foreground(ColorMuted),

		ModalBox: lipgloss.NewStyle().
			Border(lipgloss.DoubleBorder()).
			BorderForeground(ColorPrimary).
			Padding(1, 2),
	}
}
