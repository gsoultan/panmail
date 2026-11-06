# Resetting a forgotten password

An administrator who is locked out can set a new password from the server,
using the same configuration file the gateway runs with. It talks to the
database directly, so the gateway can stay up while you run it.

```sh
panmail reset-password --config /path/to/config.yaml --email admin@example.com
```

You are asked for the new password twice; nothing is echoed. It must be at
least 12 characters, the same rule as everywhere else.

| Flag | Meaning |
| --- | --- |
| `--email` | The account to reset (required). |
| `--config` | The gateway's configuration file. Optional when it is found in the default location. |
| `--password-stdin` | Read the password from the first line of standard input instead of prompting, for scripts. |
| `--disable-2fa` | Also turn off two-factor sign-in, when the authenticator is lost too. Re-enable it from your profile afterwards. |

For a script:

```sh
printf '%s\n' "$NEW_PASSWORD" | panmail reset-password --email admin@example.com --password-stdin
```

There is no flag that takes the password itself: it would end up in shell
history and in the process list.

In a container, run it inside the running one, for example
`docker exec -it <container> panmail reset-password --email admin@example.com`.
