# Setting up Docs and Slides

The pack wires Google Docs and Slides into the yard as the declared
sources `google-docs` (documents read, appended to, replaced in
place, given a picture, and laid out with a table and styled text)
and `google-slides` (decks read, slides with title and body
appended).
Each call goes out with a short-lived token minted from a gws login
on this machine, so no new copy of a secret is stored anywhere.
The login, the account, the mint and calling as a user are the four
google packs' shared setup, said once in `yardr docs google`.

1. **The gws login.** It needs the Docs and Slides scopes,
   `https://www.googleapis.com/auth/documents` and
   `https://www.googleapis.com/auth/presentations`, else the calls
   come back 403. The shared login instructions in `yardr docs google`
   start from a machine with no gws, and show how to check and add
   the `docs,slides` services while keeping the other applied packs'
   scopes.
2. **The setup card.** One card carries the pack's whole setup: the
   pack, the account and the grants. Have a crew member ask for it,
   and approve it, in footplate or in `yardr chat` (`ctrl+o`); a
   person sets up the packs they want, one card each:
   ```sh
   yardr bead ask <bead> "Set up Docs and Slides for the yardmaster?" --setup google-docs --crew yardmaster --recommend 1 --why "reading is the job; appending can follow" --context "The yardmaster reads docs and decks to work beads."
   ```
   The card shows both declarations whole with their mint, the
   account to type or take (step 3), and one option per preset
   (step 5).
   By hand it is the operator's to type, step by step, starting
   with the pack:
   ```sh
   yardr pack apply google-docs
   # applied pack google-docs to the yard: 2 created, 0 changed, 0 unchanged
   # source google-docs is declared: 6 calls under https://docs.googleapis.com (yardr source show google-docs)
   # source google-slides is declared: 2 calls under https://slides.googleapis.com (yardr source show google-slides)
   ```
3. **The account.** The yard's Google account for `google-docs`
   and `google-slides` is answered on the setup card, as described
   in `yardr docs google` (step 3): typed unseen on the first pack,
   then picked, one typed value serving both sources.
4. **The mint.** Nothing to type: the pack brings `google-mint`
   beside its declaration, and applying it (step 2) copies it for
   `google-docs` and `google-slides`, its sha256 on the card. What it needs and refuses,
   and how a mint of the operator's own replaces it, are in
   `yardr docs google`.
5. **The grants.** A pack of kind source asks none at import, so what
   a crew member may call is picked on the setup card as one preset.
   The pack's presets, as `yardr pack show google-docs` prints them
   numbered, grant both sources together:
   1. `read`: every read call of `google-docs` and `google-slides`,
      and it covers any read call either source is later given;
      changes nothing.
   2. `append`: every read call of both, `write` on `google-docs`
      and `slide` on `google-slides`: paragraphs appended at the end
      of a doc, each with its style, and slides appended to a deck,
      with title and body; replaces nothing.
   3. `everything`: what `append` grants, and `replace`, `image`,
      `table` and `style` on `google-docs`: text replaced in place,
      more than one match refused unless the call passes all=true, one
      picture placed where a placeholder text stands (the text goes),
      or at the end of the body, a table put where a placeholder text
      stands (the text goes) with its cells filled, with or without
      borders, and one match styled bold, sized or coloured, or its
      paragraph aligned.
   A grantee who comes later gets one preset grant card per source:
   ```sh
   yardr bead ask <bead> "Let the reviewers read Docs" --run "source grant google-docs --preset read --group reviewers" --context "The reviewers read docs to check a bead's work; the card names each call it covers."
   yardr bead ask <bead> "Let the reviewers read Slides" --run "source grant google-slides --preset read --group reviewers" --context "The reviewers read decks to check a bead's work; the card names each call it covers."
   ```
   By hand they are the operator's to type:
   ```sh
   for s in google-docs google-slides; do yardr source grant $s read --crew yardmaster; done
   yardr source grant google-docs write --crew yardmaster
   yardr source grant google-docs replace --crew yardmaster
   yardr source grant google-docs image --crew yardmaster
   yardr source grant google-docs table --crew yardmaster
   yardr source grant google-docs style --crew yardmaster
   yardr source grant google-slides slide --crew yardmaster
   ```
   As ada, the same grants with `--as ada`, on cards addressed to
   ada and approved by ada alone: the shared card is in
   `yardr docs google` ("Calling as a user"), one card per grant, with
   `read`, `write`, `replace`, `image`, `table`, `style` and `slide`
   from above. There is no typed way beside these: a grant `as` ada
   is ada's to give.
   `yardr source show google-docs` lists each call with its kind and
   who may make it, plain and `as` each user.
6. **Check it.** Neither source names a check call, so each check
   looks the credential over and sends nothing:
   ```sh
   yardr source check google-docs
   # google-docs: credential: GOOGLE_ACCOUNT is set in /Users/me/.yardr/sources/google.env, minted with /Users/me/.yardr/sources/google-docs/google-mint
   # google-docs: its file names no check call (check: <a read call without parameters>), so nothing was sent
   ```
7. **Try it.** As the operator, who may make any call. A doc is an
   empty file the drive source makes, whose id this pack's docs
   source then fills, so the google-drive pack is applied first and
   tried up to its own step 7. One write appends several paragraphs
   in one call, each with its style: the call reads the doc for its
   end index itself and works out each paragraph's range, and the
   first paragraph continues the doc's last paragraph:
   ```sh
   yardr call google-drive folder name="notes" parent=<a folder id>
   yardr call google-drive doc name="lunch" parent=<the folder's id>
   yardr call google-docs write docId=<the doc's id> paragraphs='[{"text":"Lunch places","style":"HEADING_1"},{"text":"Friday we ship.","style":"NORMAL_TEXT"}]'
   ```
   Text is changed in place with replace, which counts its matches
   first: more than one is refused unless the call passes all=true, and
   the answer says how many changed.
   ```sh
   yardr call google-docs replace docId=<the doc's id> find="Friday" with="Saturday"
   ```
   A picture is placed with image, in the place of a placeholder text
   typed in the doc where the picture is to go: the text is removed and
   the picture takes its place. The call reads the doc for the
   placeholder's index, and refuses a placeholder that is not there and
   one that stands in more than one place, each in words that say so.
   The match is literal and case-sensitive, found across a change of
   style inside the text, in the body of every tab; a header, a footer
   or a footnote is not searched. With no find the picture goes at the
   end of the body (the first tab's), in its last paragraph. width is in
   points, 72 to an inch, and the height follows the picture's own
   ratio; left out, Google sizes the picture from its resolution, which
   for a large one may be wider than the page.
   ```sh
   yardr call google-docs image docId=<the doc's id> uri=https://example.com/logo.png find='{{logo}}' width=120
   ```
   Google fetches the picture itself, so uri is an https address it can
   reach without a login, a public URL, of a PNG, JPEG or GIF under
   50 MB and 25 megapixels, and at most 2 kB long. A file on the machine
   the yard runs on, an http address and a private Drive file cannot be
   placed, and the call has no upload. The address is kept out of the
   record of the call, since a signed one carries its signature; Google
   saves it with the picture in the document, though, so use one that
   anyone who can read the document may see.
   A table is put with table, in the place of a placeholder text typed
   in the doc where the table is to go: the text is removed, its
   paragraph split around the table, and the cells filled from the
   call's rows. The call reads the doc for the placeholder's index,
   and refuses a placeholder that is not there and one that stands in
   more than one place, each in words that say so. The match is
   literal and case-sensitive, in the body of every tab, table cells
   included; a header, a footer or a footnote is not searched. Every
   row holds the same number of cells: at most 20 rows of 6. A cell's
   text may run to a few lines, and may itself be a placeholder that
   replace fills later.
   ```sh
   yardr call google-docs table docId=<the doc's id> find='{{prices}}' cells='[["Item","CHF"],["Bread","{{price_1}}"]]'
   ```
   The table keeps its borders unless the call passes borders=false,
   for a block of columns side by side. The call places the table and
   fills its cells in two writes; where the place lands and the fill
   fails, whether the call refuses it or the service does, the empty
   table stands: remove it, restore the placeholder, and call table
   again. The call's own refusals say so; the service's answer is only
   its status.
   Text is styled with style, which finds one match: bold=true makes it
   bold and bold=false removes bold; size sets whole points, color takes
   six hex digits, and align sets the paragraph's alignment. Table cells
   are included. At least one of the four is passed. The match
   is literal and case-sensitive, in the body of every tab; none and
   more than one are refused, each in words that say so.
   ```sh
   yardr call google-docs style docId=<the doc's id> find='Total' bold=true
   yardr call google-docs style docId=<the doc's id> find='{{price_1}}' align=right
   ```
   A slide is the same shape: an empty deck the drive source makes,
   then a slide with the caller's own id for it, unique in the deck:
   ```sh
   yardr call google-drive slides name="friday" parent=<a folder id>
   yardr call google-slides slide id=<the deck's id> slide=friday_1 title="Friday" body="We shipped."
   ```

Calling as a user — the yard reading another person's documents
with their own credential — is the shared setup too:
`yardr docs google` has it.

What is not there, on purpose: Sheets, which joins this pack when a
sheets source exists; Tasks, which is no source yet, so no pack
wires it in (Calendar is the google-calendar pack); paging past the
first page; a picture in a header, a footer or a slide, or sent up
from a file on the yard's machine; resizing or moving a picture
already placed; column widths, merged cells, and rows or columns
added to a table that stands; bullets; and no call the pack does not
list. The yard's source
format cannot express what is not listed, and the pack does not
change yardr.
