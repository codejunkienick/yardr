# Setting up Linear

The pack wires Linear into the yard as the declared source `linear`: its
webhooks are verified and read, and the yard replies, opens issues and
sets their status through the commands the pack carries. What a stage
means in Linear is a flow pack's to say, not this file's.

1. **The webhook, in Linear.** Settings → API → Webhooks: add one with the
   URL `https://<public host>/hooks/<hook name>` — the path ends in the
   hook's name, the one `yardr hook add` is given below — and tick the
   events Issues and Comments. The yard reads issue and comment deliveries
   only; anything else it cannot use.
2. **The hook and its secret.** The webhook's signing secret goes in a file,
   mode 0600, which the hook names. A new file takes its mode from the
   shell's umask, so narrow it first. The secret is never a command's
   word: that line would stay in the shell's history. Each command below
   is one thing to type, in a block of its own; run the second, type or
   paste the secret blind and press Enter:
   ```sh
   umask 077
   ```
   ```sh
   IFS= read -rs v && printf '%s' "$v" > ~/.yardr/linear.secret; unset v
   ```
   ```sh
   chmod 600 ~/.yardr/linear.secret
   ```
   ```sh
   yardr hook add linear --source linear --secret-file ~/.yardr/linear.secret --unknown allow
   # hook linear saved: POST /hooks/linear (linear)
   ```
   One hook serves every crew member. `--unknown allow` hears every
   workspace member's comment: a delivery from a sender no yard user maps
   is kept with the speaker `outside`. Give each person their identity
   (step 6) and their deliveries arrive named as them, which a
   subscription may filter with `--where speaker=<user>`.

   A crew member sets the hook up without handling the secret, by two
   asks on one bead, the secret first: it asks for the signing secret
   (`yardr bead ask <id> - --secret-hook linear --source linear`, with the
   question and where Linear shows it on stdin) and then for the hook itself
   (`yardr bead ask <id> -` with `Q:`, `C:` and `R: hook add linear --source
   linear --unknown allow` lines on stdin). The operator pastes the secret
   where Linear shows it, typed unseen; the yard's server writes it to
   `<home>/hooks/linear.secret` (mode 0600), which the hook then names,
   and it is in no note or pane. Approving the hook then makes
   `POST /hooks/linear`. `--map` and `--as`, and replacing a hook, stay
   in a terminal; the manual steps above remain available.
3. **The crew member's key.** `LINEAR_API_KEY` in an env file of the crew
   member's own, mode 0600, never in the store. Have the crew member ask
   for it, and answer on its card, in footplate or in `yardr chat`
   (`ctrl+o`), where the field shows dots:
   ```sh
   yardr bead ask <bead> - --secret linear --for-crew <name> <<'ASK'
   The token it answers with
   The token of the Linear OAuth application made for it.
   ASK
   ```
   The yard's server writes it into the crew member's env file, or with
   none set into `~/.yardr/crew/<name>.env`, which is then set as `yardr
   crew set` sets it. The running session does not see the new value:
   restart the crew member after giving it. By hand it is yours instead.
   A new file takes its mode from the shell's umask, so narrow it first.
   The token is never a command's word: that line would stay in the
   shell's history. Each command below is one thing to type, in a block
   of its own; run the second, type or paste the token blind and press
   Enter:
   ```sh
   umask 077
   ```
   ```sh
   IFS= read -rs v && printf 'LINEAR_API_KEY=%s' "$v" > ~/.yardr/crew/<name>.env; unset v
   ```
   ```sh
   chmod 600 ~/.yardr/crew/<name>.env
   ```
   ```sh
   yardr crew set <name> env_file=~/.yardr/crew/<name>.env
   ```
   The token is that of a Linear OAuth application made for the crew
   member, so it acts as the application's own user and not as a person.
   Making the application, installing it and giving the yard its client id
   and secret, which the yard mints this token from, are under The OAuth
   application, below.
   Linear takes an OAuth token as `Authorization: Bearer <token>`, and the
   pack sends it that way. A personal key makes the crew member speak as
   that person. Linear's docs (https://linear.app/developers/graphql) show
   one sent bare, as `Authorization: <key>`, and do not say that Bearer
   takes it too. The thread commands send a `lin_api_` key bare, so they
   work with either token. The declared calls (`yardr call linear viewer`,
   `yardr source check linear`) send every key as Bearer and read it from
   the source's own env file (`yardr source key linear`), so put an OAuth
   token there. Restart the crew member after changing the file.
4. **Its identity.** The id and the profile key of the user the token acts
   as, looked up in Linear by hand — both:
   ```sh
   yardr crew identity <name> --source linear --id <user id> --key <handle>
   ```
   The yardmaster files the command as a general card, which the operator
   approves; another crew member asks the yardmaster for it by mail:
   ```sh
   yardr bead ask <bead> - <<'ASK'
   Q: Who is <name> in Linear?
   C: Mentions and assignments of this id and handle in Linear should reach <name>.
   G: crew
   G: identity
   G: <name>
   G: --source
   G: linear
   G: --id
   G: <user id>
   G: --key
   G: <handle>
   ASK
   ```
   A mention is matched by the handle when the delivery carries markdown
   only, and by the id otherwise: with the id alone, the test of 11 Oct
   saw a mention arrive as `participant` instead of `mentioned`.
5. **The grants.** Opening issues and setting their status are the
   operator's to grant:
   ```sh
   yardr crew set <name> open_threads=linear set_state=linear
   ```
6. **Test it.** As the crew member: open an issue, mention it from Linear,
   move it:
   ```sh
   yardr thread open linear --in <team key> - -b -   # stdin: the title (Hello from the yard), then the body (testing the wiring)
   # comment @<handle> on the issue in Linear: it lands in the crew member's inbox as mentioned
   yardr thread state <thread id> started
   ```
   `yardr thread list <name>` shows the thread's id.
   An answer can carry files: `yardr reply <seq> --file shot.png -` (the
   words on stdin) uploads each file to Linear and posts one comment, a PNG
   or JPEG as an image and any other file as a link under its name, at most
   8 MiB or the yard's blob limit if lower. A file over that, or an upload
   Linear refuses, fails the reply with nothing posted. The crew member's
   machine needs `file` for it, which macOS and most Linux systems have,
   beside `curl` and `jq`.
   To open an issue assigned to a person, give the yard's user their
   Linear user id once, then name them:
   ```sh
   yardr user identity <user> --source linear --id <their user id>
   yardr thread open linear --in <team key> --assign <user> -   # stdin: the title (For you)
   ```
   The same identity names who spoke on their deliveries: a comment or an
   issue from them arrives with them as its speaker, and a subscription
   may filter `--where speaker=<user>`. An id names one user: one that
   another user already holds is refused.
7. **The stage map.** When a bead enters a stage, the issue it tracks under
   this source moves with it: the hook carries the map, and the crew
   member it acts as. `hook add` replaces the definition, so the whole
   map is given each time, with `--unknown allow` again:
   ```sh
   yardr hook add linear --source linear --secret-file ~/.yardr/linear.secret --unknown allow --as <name> \
       --map task:new=started --map task:review=review --map task:merged=done
   # moves tracked linear issues as <name>: task:new=started …
   ```

## The OAuth application

The crew member's Linear user is an OAuth application's own user, and the
yard mints its token from the application's client id and secret. Make the
application once, in the Linear workspace, before step 3. If the workspace
has one already, check its toggle and its install against the items below
instead of making another.

- **Create it.** Linear's new-application page is
  https://linear.app/settings/api/applications/new. The name and icon are
  how the crew member appears in the mention and filter menus, so give it
  the ones people should see. Linear asks for a redirect URL. The install
  below names it, and Linear sends the browser back there with an
  authorization `code` in the address. The yard never uses that code.
- **Switch client credentials on.** The toggle is on the application's page,
  while you create it or after. The yard's mint is that grant, and Linear's
  docs say it must be on first ("You must first toggle on client credentials
  tokens for your OAuth2 app").
- **Install it as the application, with all five scopes.** Only a workspace
  admin can, because Linear says "admin permissions are required to complete
  the installation". The admin opens Linear's authorize address with
  `actor=app`, which makes the token act as the application and not as a
  person, and with the five scopes:
  ```
  https://linear.app/oauth/authorize?response_type=code&client_id=<client id>&redirect_uri=<redirect url>&state=<any random value>&actor=app&scope=read,issues:create,comments:create,app:mentionable,app:assignable
  ```
  The first three are what the pack's calls need. The last two are Linear's
  agent scopes (https://linear.app/developers/agents). `app:mentionable`
  lets people mention the application in issues and documents, and
  `app:assignable` lets them assign it issues as the delegate.
- **Keep one scope set.** The install, the yard's mint and anything else that
  gets credentials for this application use the same five scopes. The mint
  asks for exactly those whatever a call passes it. Linear's docs give the
  reason. Requesting different scopes replaces the application's credentials
  in the workspace. Processes that share an application must use one scope
  set, or they invalidate each other's tokens
  (https://linear.app/developers/oauth-actor-authorization, "Changing an
  installation's scopes"). A client-credentials request replaces an
  authorization-code installation
  (https://linear.app/developers/oauth-2-0-authentication, "When credentials
  become invalid"). So the yard's first mint, with the same five scopes,
  takes the application's credentials over from the install. A request with
  any other set would revoke them. A new install carries the same five.
- **Give the yard the id and the secret.** Both are on the application's page
  in Linear. They go under their own names, `LINEAR_CLIENT_ID` and
  `LINEAR_CLIENT_SECRET`, in the two places step 3 gives a token. One is the
  crew member's env file, which serves replies, opened issues and status
  moves. The other is the source's own env file, which serves `yardr call
  linear` and `yardr source check linear`. With both names set, the yard
  mints a token from them and ignores a `LINEAR_API_KEY` beside them. It
  refuses one of the two without the other. Ask for them on a card, so the
  yard's server writes them and neither is a command's word or in a note:
  ```sh
  yardr bead ask <bead> - --secret linear --for-crew <name> <<'ASK'
  The Linear application's client id and secret
  Both are on the application's page in Linear.
  ASK
  ```
  The same ask without `--for-crew` gives the source's own file. At a
  terminal, `yardr source key linear LINEAR_CLIENT_ID LINEAR_CLIENT_SECRET`
  asks for each and shows nothing as it is typed. The crew member's session
  sees the new values after a restart. Rotating the secret in Linear revokes
  the application's client-credentials tokens, so give the new secret the
  same way.
- **Check it in Linear.** `yardr source check linear` mints a token and asks
  Linear whom it acts as. A pass says the token works. It does not say the
  application is offered in the menus. In a comment on any issue, type `@`
  and the application's name, then assign an issue to the application. Both
  should offer it, and the issue should show it as the delegate.
