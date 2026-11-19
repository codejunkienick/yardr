# Setting up a routine

The pack wires a person's routine into the yard beside their phone: what
lands in their inbox is POSTed, once per batch, to a URL of theirs —
below, an n8n workflow — with the words of the phone push and, beside
them, what the batch is about as numbers. The phone is told as before;
the routine is a second telling of the same message, best-effort, and
either stands without the other.

Nothing here sends a picture, and the URL is told nothing else: its path
is its only credential, so keep it as a password. It stands in no file
you write by hand, and in no error, event or log of the yard.

1. **The workflow and its URL (n8n).** In n8n, start a workflow with a
   *Webhook* node: *HTTP Method* `POST`, *Authentication* `None`, and the
   path as n8n made it — that path is the secret. Publish the workflow
   and copy its *Production URL* whole. Add what the routine does with
   the body after the node (a message, a row, a branch on `items`): the
   yard sends one POST per batch,

   ```json
   {"title": "mail from ada", "text": "the first words…", "items": [{"kind": "mail", "seq": 412}]}
   ```

   `title` and `text` are the phone's words. Each of `items` is one
   thing the batch is about: `{"kind":"mail","seq":412}` for mail,
   `{"kind":"ask","bead":"yardr-example","ask":91}` for an ask, or
   `{"kind":"notification","seq":413,"bead":"yardr-example","note":"closed","source":"github"}` for a notification of a bead. A secret ask is told by its
   number alone on both tellings: the title `A question waits` and the
   text `Question <ask> waits for you.`, with no question and no asker.
2. **Apply the pack.** As the operator:

   ```sh
   yardr pack apply routine
   # source routine is declared: the URL in ROUTINE_URL of each person told (yardr source show routine)
   ```

   The source declares no call: there is nothing to grant, and its check
   sends nothing. Telling is the yard's, beside the phone.
3. **Name the routine as the person's second telling.** The user
   themselves, or the operator for them, in a terminal:

   ```sh
   yardr user set mckean webhook=routine
   # user mckean: told outside the yard: through pushover and routine
   ```

   Put the told person's name for `mckean`. `webhook=` with nothing
   after it ends the second telling, and what waited is kept for the
   phone. The parts told of are the phone's (`yardr user set <user>
   tell=…` names both tellings at once).
4. **Give the yard the URL.** No file is written by hand: the command
   asks for the URL and shows nothing as it is typed or pasted, and the
   yard's server writes it to the person's file only they can read
   (`~/.yardr/users/mckean.env`):

   ```sh
   yardr source key routine --as mckean    # ROUTINE_URL, the workflow's production URL: the person's to give
   ```

   Or have a crew member ask for it, and answer on its card, in
   footplate or in `yardr chat` (`ctrl+o`), where the field shows dots:

   ```sh
   yardr bead ask <bead> - --secret routine --as mckean <<'ASK'
   Your routine's URL
   The n8n workflow's Production URL, Webhook node.
   ASK
   ```

   A member's URL must be a public `https://` name (a name with a dot,
   not an address of a machine, not this machine): the yard refuses
   another at the send, without naming it. An operator's own routine
   may be on this machine, as any address the operator gives may.
5. **Something to be told of.** Mail to the user, or an ask for them,
   past its 20 seconds still unread:

   ```sh
   yardr mail mckean -
   ```

   The phone buzzes as it did, and the workflow runs once with the body
   above. `yardr user show mckean` has how the last message ended on
   each telling (`last told` for the phone, and the routine's beside
   it). A routine that is down or answers no success is logged and
   recorded there, and the phone is told all the same, once.

**Rotation** is a new URL given the same way: publish the workflow's new
path (or a new workflow), and give its Production URL again with step
4's command or card. The new value replaces the old in the same file,
and the next batch goes to it. An URL given for another host is still
the person's to replace: nothing of the old one is kept to compare.
