# broken-web scenario

A deliberately misconfigured web gateway, used to demonstrate the Praxis DevOps
agent.

**Symptom:** health checks to `http://127.0.0.1:8080/health` fail with
"connection refused".

**Expected:** the gateway must listen on **port 8080**.

**Planted bug:** `nginx.conf` sets `listen 9090;` instead of `8080`, and
`app.log` shows the resulting connection-refused errors.

Run the agent against this directory:

```sh
praxis run -C examples/broken-web "diagnose why the web service is unreachable on port 8080"
```

To let the agent apply the fix (edit the config), add `-allow` for any commands
it may run, or let it use `write_file` directly:

```sh
praxis run -C examples/broken-web "fix the config so the gateway listens on 8080"
```
