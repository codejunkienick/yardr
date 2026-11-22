# Google in the yard

Four packs wire Google in, grouped by what a person turns on: each
is applied alone, and a yard takes only the Google access it needs.

| pack | wires in | the login's services |
|---|---|---|
| `google-mail` | Gmail read, drafts listed, written and edited, never sent (`google-mail`) | `gmail` |
| `google-drive` | Drive files and folders, shared with one address or copied whole, the empty Docs and Slides the docs pack fills, and comments read and replied to (`google-drive`) | `drive` |
| `google-docs` | Docs read, appended to, replaced in place, given a picture, and laid out with a table and styled text, Slides read and appended to (`google-docs`, `google-slides`) | `docs,slides` |
| `google-calendar` | Events listed, read, created and edited without invitations, calendars listed (`google-calendar`) | `calendar` |

Each pack's own steps are its setup notes (`yardr pack show
google-mail`, `yardr pack show google-drive`, `yardr pack show
google-docs` and `yardr pack show google-calendar`): the pack itself
(step 2), its grants (step 5), and the operator trying what they set
up (steps 6 and 7). This page has what the four share: the gws login
every call mints from (step 1), the account (step 3), the mint (step
4), and calling as a user. The steps
use `ada` for a second person, `mckean` for the operator's user name
and `yardmaster` for the crew member; replace each with this yard's
where they differ.

Two identities can call. The yard's own account answers the plain
calls, `yardr call google-mail search …`, made with the credential
the yard holds for each source. A person's own account answers the
calls made as them, `yardr call google-mail search --as ada …`, made
with the credential in their user file. The steps below set the
yard's account up first, then one person's, ada's; the operator's own
is a second person of the same shape, with an address of its own: an
address belongs to one holder, so the yard's address is never also a
person's (step 3).

Everything a crew member asks for — a user added, a pack's whole
setup, a grant plain or `as` — is a card, answered in
footplate or in `yardr chat` (`ctrl+o`). Setting a pack up is one
card per pack: the apply, the account and the grants together
(`bead ask --setup`), the address typed once, then picked. The
sign-in itself (step 1) happens on the machine that signs in, whose
browser it opens, and reaches the yard pasted through a card; what
stays in the yard's terminal is the operator trying what they set
up (steps 6 and 7). The mint (step 4) comes with the pack.

## Step 0. The person is a user

Nothing can be asked of ada or granted as her until she is a user of
the yard: both refuse a name it has not. Have a crew member ask for
it, and approve its card:

```sh
yardr bead ask <bead> - <<'ASK'
Q: Add ada as a user of the yard
C: Ada's Google account is set up next; the yard needs her as a user first.
R: user add ada
ASK
```

The card is the operator's to approve (`yardr bead answer <bead>
<ask> --approve`, or the card's button in footplate). By hand it is
one command:

```sh
yardr user add ada
```

The yard's own account needs no user; the operator needs one only
to be called as (step 3).

## Step 1. The gws login

The four packs are optional: a yard without Google needs none of
this. What follows starts from a machine with no gws: the machine
that signs in, which is your own unless the yard runs on it too.
The yard's machine needs gws on PATH as well, for the mint.

Install gws first, from its install page
([github.com/googleworkspace/cli](https://github.com/googleworkspace/cli)):
a release binary for Linux or macOS, and on macOS from Homebrew too:

```sh
brew install googleworkspace-cli
```

(`npm install -g @googleworkspace/cli` fetches the same release.)
Whatever way gws came by, it answers from PATH. A yard with a google
pack applied names a missing gws itself, in `yardr doctor` and on the
pack's setup card, and `yardr pack install <pack>` installs it, in a
terminal or on a run card the operator approves:

```sh
command -v gws
gws --version
```

Then configure the GCP project and OAuth client once, on the machine
that signs in, for its default gws config. `gws auth setup` needs the gcloud CLI installed and
logged in with `gcloud auth login` (Application Default Credentials from
`gcloud auth application-default login` do not authenticate gcloud's own
commands). It uses gcloud's current project by default; pass
`--project <id>` to choose another:

```sh
gws auth setup
```

Without gcloud it refuses outright. It walks through enabling the Gmail,
Drive, Docs, Slides and Calendar APIs the four packs call, configuring
the OAuth consent screen and client, and writes the client to
`~/.config/gws/client_secret.json`.

A second person signs in on their own machine the same way, in its
default config; no second setup is run for them.

The sign-in opens a browser on the machine that runs it and waits
there, so a hosted yard cannot lead it: the person signs in on a
machine of their own, exports the sign-in there, and pastes the
export into a card. The yard's calls mint from what the card gives.
On the machine that signs in:

```sh
gws auth login -s gmail,drive,docs,slides,calendar
gws auth status
gws auth export --unmasked
```

`gws auth status`'s `user` is the account's address, which step 3
sets as `GOOGLE_ACCOUNT`. Its `scopes` must hold the scopes the
packs call with — `https://www.googleapis.com/auth/gmail.modify`
for mail, `https://www.googleapis.com/auth/drive` for drive,
`https://www.googleapis.com/auth/documents` and
`https://www.googleapis.com/auth/presentations` for docs, and
`https://www.googleapis.com/auth/calendar` for calendar — else the
calls come back 403. When a pack is added, log in again with every
service the yard wants, so nothing set up is lost. A yard with one
pack logs in with that pack's services alone (the table above);
adding a pack later means logging in again with the services
added, and giving the new export as below. Google's token narrows
by this grant, not by call, so the login's scopes are the yard's
whole Google access.

Give the sign-in to the yard by card: one card for the yard's own
account, answered by the operator, and one for a person's own,
answered by that person alone — the export holds their refresh
token, so nobody else pastes it, the operator included. Whoever
sets Google up asks; a crew member may ask for either, as it asks
for hook secrets:

```sh
yardr bead ask <bead> - --secret-google <the account gws auth status showed> <<'ASK'
The yard's Google sign-in
Sign in with gws auth login on your machine, then paste gws auth export --unmasked.
ASK
yardr bead ask <bead> - --secret-google <ada's account> --as ada <<'ASK'
Your Google sign-in
Sign in with gws auth login on your machine, then paste gws auth export --unmasked.
ASK
```

Which door gives which: the yard's own account is the operator's
to give — on a hosted yard, from the card's page in the hosted web
(where the field shows dots, typing first and signing in fresh
after), from a paired device, or in a terminal (`yardr bead answer
<bead> <ask> --secret`). A person's own account is that person's
alone to give, and the web page gives it for nobody: ada gives hers
from a paired device or in a terminal with the same command, never
on the web page and never through the operator. Down the edge link
her device carries it only where it was paired with the QR: a
device sealed in place does not carry it there, since the export
is a refresh token that is written to disk and lasts, where her
sign-in code is used once and kept in memory. Either way, paste
the whole output of `gws auth export --unmasked` where the field
shows dots. The yard checks it is that export and writes it, mode
0600, to the account's credentials file: the yard's own at
`~/.config/gws/credentials.json`, a person's at
`~/.config/gws-accounts/<account>/credentials.json`, each in the
home of the user the yard's server runs as. Anything else is
refused, naming the export to paste. So is a sign-in for an
address another holder already has as its `GOOGLE_ACCOUNT` (step 3):
that address's file is theirs, and only they give it. The export is
in no note, event or answer the yard keeps.

On a yard you run yourself, a login typed in its terminal works
exactly as before instead: log in there as above, with no card.
The mint reads the pasted credentials file where one stands for
the account, and the login where none does (step 4).

## Step 3. The account

`GOOGLE_ACCOUNT` is bound: each address belongs to one holder, the
yard or one person, and no other holder may be given it or call with
it. A giving that names an address another holder has is refused,
naming the holder and the giving that frees it, and a call made as a
person whose file names another holder's address is refused the same
way, a file edited by hand included. Give each holder an address of
their own.

The yard's own first: one card per pack, each naming
`GOOGLE_ACCOUNT`, the address step 1 showed. A crew member asks,
and the operator answers where the field shows dots. The first
pack's card is typed unseen, into that pack's first source's own
file (`~/.yardr/sources/google-mail.env`), which the yard chooses
and records, so no `env_file` is set by anyone. Every pack after
lists the first by name with its address, to take it or type
another: the address is typed once, then picked. Run only the cards
for the packs the yard uses:

```sh
yardr bead ask <bead> - --setup google-mail --crew <name> --recommend 1 <<'ASK'
Q: Set up Gmail for the crew?
C: The address step 1 showed: the address alone, no token.
W: reading is the job; drafts can follow
ASK
yardr bead ask <bead> - --setup google-drive --crew <name> --recommend 1 <<'ASK'
Q: Set up Drive for the crew?
C: The address step 1 showed: the address alone, no token.
W: reading is the job; making and sharing can follow
ASK
yardr bead ask <bead> - --setup google-docs --crew <name> --recommend 1 <<'ASK'
Q: Set up Docs and Slides for the crew?
C: The address step 1 showed: the address alone, no token.
W: reading is the job; appending can follow
ASK
yardr bead ask <bead> - --setup google-calendar --crew <name> --recommend 1 <<'ASK'
Q: Set up Calendar for the crew?
C: The address step 1 showed: the address alone, no token.
W: reading is the job; creating can follow
ASK
```

Each names the grantee and recommends a preset with why, as any
setup ask does; the numbers count the pack's presets as `yardr pack
show` prints them, which each pack's own step 5 says.

The second card reads `the account GOOGLE_ACCOUNT: already given
for google-mail (GOOGLE_ACCOUNT=…, in
~/.yardr/sources/google-mail.env). Take it with --account
google-mail, or type another unseen`, answered `yardr bead answer
<bead> <ask> <n> --account google-mail` or `<n> --secret`. Taking
it points the new pack's sources at the listed file, which
`yardr source show` says; typing another writes to the new pack's
own file. There is no silent reuse, not even with a single match:
the answer names a candidate or types another. For google-docs,
whose two sources share the one account, the first card's typed
value is written once to `~/.yardr/sources/google-docs.env` and
both sources point at it, the file named on the card.

Each card asks for `GOOGLE_ACCOUNT` and never for `GOOGLE_TOKEN`,
which is minted per call, never given.

By hand the five share one file instead of one per pack. The
address is no secret, so it is typed as usual; the file still goes
0600, as every env file. A new file takes its mode from the
shell's umask, so narrow it first. Each command below is one thing
to type, in a block of its own.

```sh
umask 077
```

```sh
printf 'GOOGLE_ACCOUNT=%s\n' '<the address step 1 showed>' > ~/.yardr/sources/google.env
```

```sh
chmod 600 ~/.yardr/sources/google.env
```

```sh
for s in google-mail google-drive google-docs google-slides google-calendar; do yardr source set $s env_file=~/.yardr/sources/google.env; done
```

Run only the sources of the packs the yard uses. Either way the
file gains no second name, and a file already holding
`GOOGLE_ACCOUNT` alone needs nothing more given or set again.

A person's is one card however many sources: one line in
`~/.yardr/users/ada.env` serves every applied pack's sources. It is
asked of ada, or of the operator for her, and no `env_file` is set,
since a call `as ada` reads her file by her record. The card names
`google-mail`; with another pack alone applied it names that pack's
source instead:

```sh
yardr bead ask <bead> - --secret google-mail --as ada <<'ASK'
Your Google account for your mail
The address of the login step 1 made for you: the address alone, no token.
The yard reads your Gmail as you once you grant it (step 5).
ASK
```

This is `--as`, not `--for-user`: that asks for the address in a
source that tells people, and this for the credential a call made
as them reads. By hand it is typed unseen:

```sh
yardr source key google-mail --as ada
# ada gave GOOGLE_ACCOUNT · it is set in ~/.yardr/users/ada.env · yardr source check google-mail --as ada
```

The operator called as needs an address of his own: once he is a
user (step 0), the same card with `--as mckean` gives his second
address into `~/.yardr/users/mckean.env`, with its own login under
step 1's directory for that address. The yard's address cannot be
given again as his — it is the yard's already, and the giving is
refused — so calls as him take a second Google account, or are not
set up at all: with only the yard's address, drop the `--as` card
and make the plain calls.

```sh
yardr bead ask <bead> - --secret google-mail --as mckean <<'ASK'
Your Google account, to be called as
Your second address, not the yard's: the address alone, no token.
Kept apart from the yard's own: one address for its calls, one for calls as you.
ASK
```

`yardr source show google-mail` says of each user whether their
names are set (`credential as ada: …`), and `yardr user show ada`
names the sources holding a credential of hers — never a value.

## Step 4. The mint

Nothing to install or set: each google pack brings `google-mint`,
the command that turns a login into a token per call, beside its
declarations (`credential.mint_command: [google-mint]`). Applying
the pack copies it to `~/.yardr/sources/<source>/google-mint`, mode
0700, for each of its sources, and the apply card names it with its
sha256 on the source's credential and command lines: approving the
card approves those bytes. `yardr source show google-mail` says
which mint runs (`mint: google-mint, the declaration's own …`) and
the copy's sha256.

Each pack carries its own copy, the same bytes in all four: a pack
is applied alone, and a command's program is a file inside its own
pack, so no pack depends on another being applied. A test holds the
four copies to one text.

It needs `gws` and `curl` on PATH (`GWS_BIN` names another gws).
There is no per-user mint: one command mints every account,
choosing the login by the account it is given — the default login
where it is for that account, else that account's own login in
`~/.config/gws-accounts/<account>`. Where a sign-in was given by
card (step 1), the account's credentials file stands in those same
places: the gws the mint starts for that account is given it
through `GOOGLE_WORKSPACE_CLI_CREDENTIALS_FILE` for `gws auth
status`, and where status confirms the account the mint reads the
file itself for the exchange — `gws auth export --unmasked` never
reads that file, so a login of another account beside it is never
minted. Where none stands, the login is read exactly as before. It refuses an account
with neither, naming the login command that makes it, and a login
for another account; it never prints the refresh token or client
secret, nor writes them anywhere. No new copy of a secret is
stored: the token is handed to each call under `GOOGLE_TOKEN` and
kept in memory alone.

Which program runs with a person's login stays the operator's:

- A pack that later brings another `google-mint` uses one drift card:
  a setup ask on the applied pack (`yardr bead ask <bead> - --setup <pack>
  --crew <name> --recommend 1`, with `Q: Take the new <pack>?`, `C: Review
  the new mint while keeping the existing account.` and `W: reading is the
  job; keep new calls narrow` on stdin) shows
  the copy's old and new sha256 with the diff beside the account as
  it stands and each preset held and new, and nothing is copied until
  the operator approves it (or types `yardr pack apply` and says yes)
  ([chat.md](chat.md#setting-up-a-packs-sources)).
- A mint the operator sets by hand wins over the pack's, and
  applying the pack again keeps it:

  ```sh
  yardr source set google-mail mint_command=/Users/me/bin/my-mint
  ```

  `mint_command=` with nothing after it takes it away, and the
  pack's mints again. Setting one is the operator's alone, typed:
  no card and no agent sets it.

## Calling as a user

The yard can call as another person of the yard, with their own
credential: the yardmaster reading ada's mail, not the operator's.
It takes three things, beside the pack and its grants:

1. The person is a user of the yard (step 0), and their Google
   account is signed in (step 1), with the scopes the calls need:
   pasted by card from their own machine, or a login of its own on
   the yard's machine. The mint reads that sign-in for their calls
   and the yard's own for the yard's.
2. Their account's address goes into their env file, not the
   source's: one `--as` card, however many sources (step 3).
3. The grant as them is theirs alone to give, on a card addressed
   to them and approved by them alone — the yardmaster asks, ada
   allows, and the operator cannot allow it for her:
   ```sh
   yardr bead ask <bead> - --to ada <<'ASK'
   Q: Let the yardmaster read your Gmail as you
   C: The yardmaster reads your mail as you when your beads need it.
   C: You approve this yourself; the operator cannot approve it for you.
   R: source grant google-mail read --crew yardmaster --as ada
   ASK
   ```
   `--to ada` is what addresses it to her: without it the card is
   refused. Each update call `as` her is its own card the same way,
   with the call's name from that pack's step 5 — for Gmail,
   `draft_edit` is its own grant apart from `draft`. There is no typed
   way beside these: a grant `as` ada is ada's to give, so the
   operator typing one is refused with the card that asks her. A
   plain grant covers no call as them. Anyone may take a grant
   back, as any grant.

```sh
yardr source check google-mail --as ada   # her credential, tried as a check tries the source's
yardr call google-mail search --as ada q="from:me" maxResults=5
```

How a call as a user is granted, made and counted is the same for
every source: [integrations.md](integrations.md#a-call-by-a-session-or-a-crew-member)
has it.
