# Open Content migrations

Goose `.sql` migrations, embedded in the binary and run by `app migrate`. They
run after core's, and their versions are tracked in
`public.goose_db_version_opencontent`, the ledger core used while Open Content
was its module.

Never edit a migration that has been merged; add a new one.

Create new tables as `open_content.<name>`. Migrations run as core's database
user, so an unqualified table lands in `public`, where the `open_content` role
can't use it.
