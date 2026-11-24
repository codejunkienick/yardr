package store

import (
	"fmt"
	"strings"
	"unicode"
)

// upgradeSettings are the settings of yardr upgrade and of the
// release check the daemon runs beside it (internal/dispatch): what
// the yard installs itself from, whether it restores on failure, how
// it signs what it installs, and whether it mails when a newer
// signed release is out. They stand in their own file so settings.go
// keeps room for the rest.
var upgradeSettings = []Setting{
	{Key: UpgradeFromSetting, Help: "the depot whose landed build the yard installs itself after the assembly lands on it (yardr upgrade); unset, the assembly installs nothing and a hand run with no depot named takes the latest signed release. " +
		"It names a depot of the yard: a depot the yard has not got is refused, and the named depot is not removed until the setting is cleared",
		Check: func(v string) error { return checkName("depot name", v) }},
	{Key: UpgradeRestoreSetting, Help: "whether a failed yardr upgrade restores the pre-install store copies (true) or leaves refused stores down for a person to restore (false); default: true",
		Default: "true", Env: "UPGRADE_RESTORE", Check: func(v string) error {
			if v != "true" && v != "false" {
				return fmt.Errorf("%s %q: true or false", UpgradeRestoreSetting, v)
			}
			return nil
		}},
	{Key: CodesignIdentitySetting, Help: "the code-signing identity yardr upgrade signs the installed binary with on darwin, under the identifier dev.yardr.yardr, so a Developer Tools entry holds across reinstalls; unset, nothing is signed",
		Check: func(v string) error {
			// The value reaches a command line as one argument, never a
			// shell, so only what would not survive the argument is
			// refused.
			if strings.IndexFunc(v, unicode.IsControl) >= 0 {
				return fmt.Errorf("%s %q: no newline or control character", CodesignIdentitySetting, v)
			}
			return nil
		}},
	{Key: AutoUpgradeSetting, Help: "whether the daemon mails the operator and the yardmaster when a newer signed release is out (notify), or does nothing (off); the daemon reads it on every pass; default: off. " +
		"A coming value, release, will install the release on its own; it is refused until that lands",
		Default: "off", Check: func(v string) error {
			if v != "off" && v != "notify" {
				return fmt.Errorf("%s %q: off or notify", AutoUpgradeSetting, v)
			}
			return nil
		}},
}
