package claude

import "errors"

// errNoTerminal is returned when an action needs a live terminal and the
// session has none.
var errNoTerminal = errors.New("claude: session has no live terminal")
