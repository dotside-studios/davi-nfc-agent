package traymenu

// Spec is the state a menu item is created with. Platforms fix some of this at
// creation — whether an item is a checkbox, most notably — which is why it is
// passed in one go rather than set afterwards.
type Spec struct {
	Title    string
	Tooltip  string
	Checkbox bool
	Checked  bool
}

// Native is one platform menu item, as the driver represents it.
//
// Implementations are only ever handed values they created themselves, so a
// driver is free to type-assert its own type back out of the interface.
type Native interface {
	SetTitle(title string)
	SetTooltip(tooltip string)
	SetChecked(checked bool)
	SetEnabled(enabled bool)
	SetVisible(visible bool)

	// Clicks reports activations of this item. A driver may drop a click when
	// nobody is receiving, so [Menu] keeps a receiver on the channel for the
	// item's whole life. Returning nil means the item can never be clicked.
	Clicks() <-chan struct{}
}

// Driver is the platform a [Menu] draws itself on.
//
// The interface exists so the menu can be built and driven headless: it is the
// entire surface this package touches outside its own types.
type Driver interface {
	// Run shows the tray icon and calls onReady once the platform is ready to
	// take menu items. It blocks until Quit is called, then calls onExit.
	Run(onReady, onExit func())
	Quit()

	SetIcon(icon []byte)
	SetTooltip(tooltip string)

	// AddItem adds an item under parent, or at the top level when parent is
	// nil. Items cannot be removed once added on every supported platform, so
	// there is deliberately no counterpart: see [NewList] for menus whose
	// contents change.
	AddItem(parent Native, spec Spec) Native
	AddSeparator(parent Native)
}
