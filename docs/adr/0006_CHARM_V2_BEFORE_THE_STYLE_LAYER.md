# Charm v2 before the style layer

The post-v0.1 UX phase (colors, floating overlays) begins with a mechanical
migration to the charm v2 ecosystem — bubbletea v2, lipgloss v2, bubbles v2,
teatest/v2 — before any styling lands. A "color pass" opening with a framework
migration is deliberate: the theme layer we are about to build would otherwise
sit on APIs lipgloss v2 removed (`AdaptiveColor` is gone, replaced by explicit
background detection through the program plus a light/dark helper), and the
floating overlays we decided on would need a hand-rolled ANSI-splice compositor
that v2's Canvas/Layer makes native. Building the style layer once, on the
current major, beat both alternatives we considered: staying on v1 and
hand-rolling (style layer built twice), or downgrading overlays to inline
bordered panels (avoids the compositor but still builds the theme on a
superseded major). The migration PR must be visually silent — the existing ASCII
golden frames coming out byte-identical is its acceptance test.
