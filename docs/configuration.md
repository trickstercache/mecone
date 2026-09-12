# Mecone configuration file

Mecone runs without a configuration file. When you pass one with `-config`, it does two things:
it supplies defaults for the settings you would otherwise type as flags, and — if it names any
targets — it turns a single run into a run over a whole catalog of proxies, each started, tested
and shut down by Mecone.

```bash
bin/mecone run -config catalogs/industry-wide/catalog.yaml -json results.json -yaml results.yaml -md report.md -csv rawdata.csv
```

> **Status: new.** The catalog, the target lifecycle and the CSV output landed recently and have
> seen little use outside this repository. Expect rough edges, especially around proxies that are
> slow or unusual to start.

The file is YAML and decodes strictly: an unknown key is a load error, not a silently ignored
one. Every section is optional. Validation reports every problem at once, so a file with three
mistakes fails with three messages.

```yaml
run:      # defaults for the settings flags also control
origin:   # how the origins Mecone starts are bound and advertised
targets:  # the catalog of proxies to test
```

Relative paths inside the file — `run.suites_dir`, a file's `source`, a process `workdir` — are
resolved against the directory holding the configuration file, not the current directory.

## What a file with no targets does

A file with only a `run:` section is a place to keep the flags you always pass. `run` and
`client` behave exactly as they do without it, and you still choose the proxy with `-target`.

A file that *does* define `targets:` takes over: `run` and `client` test every target in the
catalog instead, and `-target`, `-control` and (for `run`) `-listen` and the TLS listener flags
are ignored, because Mecone gives every target its own origin.

## The run section

Each of these settings has a flag. **A flag given on the command line always wins**, whether or
not it matches the default; a setting the command line left alone comes from the file.

| Field | Flag | Meaning |
|---|---|---|
| `protocols` | `-proto` | Client protocols to test over: `h1`, `h2`, `h2c` |
| `suites` | `-suites` | Suite IDs to run; empty selects every suite |
| `tests` | `-tests` | Test IDs or glob patterns, such as `storage-*` |
| `levels` | `-levels` | Requirement levels to include: `must`, `should`, `may`, `info` |
| `suites_dir` | `-suites-dir` | Load suites from this directory instead of the built-in set |
| `concurrency` | `-concurrency` | How many tests run at once, per target |
| `target_concurrency` | *(none)* | How many targets are tested at once; default `10` |
| `timeout` | `-timeout` | Per-request timeout, as a duration string such as `30s` |
| `insecure` | `-insecure` | Skip TLS certificate verification of the proxy under test |

Only a value that is actually set carries over: `concurrency: 0` and `insecure: false` are the
same as leaving the field out. `-json`, `-yaml`, `-csv`, `-md` and `-format` have no file equivalent.

```yaml
run:
  protocols: [h1, h2c]
  suites: [caching, ranges]
  levels: [must, should]
  concurrency: 8
  target_concurrency: 1
  timeout: 30s
```

Setting `target_concurrency: 1` runs one proxy at a time. That is slower, but it keeps a proxy's
timings from being affected by whatever else is running on the machine. It never changes the order
of anything: a report lists its targets in catalog order however they were scheduled, and whichever
one finished first.

One caution on `protocols`: `h2` requires a TLS target, and Mecone always reaches a `docker:` or
`process:` target over `http://`. Selecting `h2` for a catalog of managed targets fails those
targets outright rather than skipping the protocol. Use `h1` and `h2c` for managed targets, and
keep `h2` for a `proxy:` target with an `https` URL.

## Notice

`notice` is one line about the catalog that every report prints under its scores, so context a
reader of the numbers needs is not left to the documentation:

```yaml
notice: >-
  Skipped and failed tests here are the absence of a proxy hop, not defects in Mecone.
```

It reaches the terminal summary, the Markdown report and the JSON one, and is stored in the
results file (JSON or YAML), so `mecone report` still shows it when scoring that file later.

## The origin section

In catalog mode Mecone starts one origin per target, on a port the operating system picks at
the moment the target starts. This section controls only *where* those origins are bound and
what address a target is told to use for them.

| Field | Default | Meaning |
|---|---|---|
| `listen` | `127.0.0.1`, or `0.0.0.0` when any target joins a named docker network | The interface every per-target origin binds |
| `advertise` | chosen per target (see below) | The host a target uses to reach its origin, as `${ORIGIN_HOST}` |

The port is never configurable: it is ephemeral, and different for every target and every run.

The client always reaches the control plane at `127.0.0.1:<origin port>`, directly rather than
through the proxy. If you set `listen` yourself, use `0.0.0.0` rather than one specific address,
or the control plane becomes unreachable.

When `advertise` is not set, the host a target is given is:

| Target | `${ORIGIN_HOST}` |
|---|---|
| `proxy:`, or `process:` | `127.0.0.1` |
| `docker:` on the host network, on Linux | `127.0.0.1` |
| `docker:` on the host network elsewhere, or published-port bridge networking | `host.docker.internal` |
| `docker:` on a named docker network | this machine's hostname |

## The targets catalog

Each entry in `targets:` is one proxy.

| Field | Required | Meaning |
|---|---|---|
| `name` | yes | Unique in the catalog. Starts with a letter or digit, then letters, digits, `.`, `_` or `-`. It names the target in reports and in container names. |
| `proxy:` / `docker:` / `process:` | exactly one | How Mecone reaches the proxy, and whether it manages its lifecycle |
| `readiness:` | no | How Mecone decides the proxy is serving |
| `settle:` | no | A pause after the proxy reports ready, before its tests start, such as `250ms` |

Which of the three blocks you use decides everything else:

| Block | Mecone starts and stops it | Reaches it at |
|---|---|---|
| `proxy:` | no | the URL you give |
| `docker:` | yes, as a container | a port on this machine, or the container's name on a shared docker network |
| `process:` | yes, as a local command | `127.0.0.1` on a port Mecone picks |

### proxy

An endpoint that is already running and that Mecone leaves alone.

| Field | Required | Meaning |
|---|---|---|
| `url` | yes | Absolute `http` or `https` base URL of the proxy. `${...}` values are expanded. |
| `insecure` | no | Skip TLS certificate verification for this target, as `-insecure` does for all of them |
| `origin_port` | no | Port this target's origin binds. Without it the port is chosen fresh each run. |

Mecone does not write the configuration of a proxy it does not manage, so such a proxy cannot
learn an origin port that changes every run. Set `origin_port` to the port the proxy already
forwards to, and this target's origin binds exactly that port:

```yaml
targets:
  - name: staging
    proxy:
      url: https://proxy.example.com
      origin_port: 8000       # the proxy's configured upstream
```

Give each unmanaged target its own port, since every target gets its own origin. If the port is
already in use the target is recorded as failed rather than tested against the wrong origin. A
proxy that reaches Mecone across a network also needs `origin.listen` and `origin.advertise`.

### docker

A container Mecone runs for the duration of the target's tests and stops afterwards.

| Field | Required | Meaning |
|---|---|---|
| `image` | yes | Image name, without a tag |
| `tag` | no | Image tag; without one the image reference is used as written |
| `network` | no | `host`, `bridge`, or the name of a docker network. See below for the default. |
| `container_port` | no | The port the proxy listens on inside the container; default `8080`. Applies only when the container joins a named docker network. |
| `command` | no | Overrides the image's entrypoint arguments; expanded |
| `args` | no | Appended after `command`; expanded |
| `env` | no | `KEY=value` strings, one per `--env`; expanded |
| `volumes` | no | Raw `--volume` arguments, such as `cache:/var/cache/nginx`; expanded |
| `pull` | no | `missing` (default), `always` or `never` |
| `files` | no | Configuration files to render and mount read-only; see [Files](#files) |

The container is started detached, named `mecone-<name>-<8 hex digits>`, and removed when it
stops. On failure, the last 40 lines of `docker logs` are attached to the error.

#### Network modes

A proxy under test has to reach its origin, and Mecone has to reach the proxy. How that is
arranged depends on where docker and Mecone are running, so the default adapts:

| `network` | What Mecone does | Reaches the proxy at |
|---|---|---|
| unset, on Linux | `--network host` | `127.0.0.1:${LISTEN_PORT}` |
| unset, elsewhere | `--publish 127.0.0.1:PORT:PORT` and `--add-host host.docker.internal:host-gateway` | `127.0.0.1:${LISTEN_PORT}` |
| `host` | `--network host` | `127.0.0.1:${LISTEN_PORT}` |
| `bridge` | publishes the port, as above | `127.0.0.1:${LISTEN_PORT}` |
| a name, such as `lab` | `--network lab` | `http://<container name>:${LISTEN_PORT}` |

The first two rows are the default: containers share the host's network on Linux, where that
works, and elsewhere the proxy's port is published to this machine. The same catalog therefore
runs on Linux, macOS and Windows without being edited.

Name a docker network only when **Mecone itself is running as a container on that network**. In
that mode the proxy is reached by container name, `${LISTEN_PORT}` is `container_port` rather
than a port Mecone picked, and every per-target origin binds `0.0.0.0` so the containers can
reach it.

### process

A local command Mecone starts and stops around the target's tests. The command runs through
`/bin/sh -c`, in its own process group, so a proxy that forks workers is stopped with them. That
makes `process:` targets POSIX-only; a catalog of `docker:` targets runs anywhere docker does.

| Field | Required | Meaning |
|---|---|---|
| `start` | yes | The command that runs the proxy; expanded |
| `stop` | no | A command that shuts the proxy down; `${PID}` is the process Mecone started. Without it, Mecone signals that process. |
| `background` | no | `true` when `start` returns as soon as the proxy is running, rather than running in the foreground. `stop` is then required, since there is no process left to signal. |
| `workdir` | no | Directory the command runs in, and where its `files:` are rendered; default is the target's temporary working directory. A relative path resolves against the configuration file. |
| `env` | no | `KEY=value` strings added to the environment; expanded |
| `files` | no | Configuration files to render; see [Files](#files) |

A foreground proxy is stopped with `SIGTERM` to its process group (or your `stop` command), and
`SIGKILL` five seconds later if it has not exited. The last 8 KiB of its output is kept and
attached to a startup or readiness failure.

`${PID}` is only meaningful for a foreground proxy, and using it with `background: true` is a
configuration error: no process is left to name, so it would expand to `0`, which a shell reads
as every process in Mecone's own group. For a backgrounded proxy, write a `stop` that finds it
some other way, such as a pidfile under `${WORKDIR}`.

### readiness

Before any test runs, Mecone polls the target until it answers. The default probe fetches the
origin's control-plane info endpoint **through the proxy**, which proves the whole path —
client to proxy to origin — rather than just that a port is open.

| Field | Default | Meaning |
|---|---|---|
| `path` | `/_mecone/v1/info` | Path requested on the target's base URL; expanded |
| `status` | `[200]` | Status codes that count as ready |
| `timeout` | `30s` | How long to keep trying before the target is recorded as failed |
| `interval` | `250ms` | Wait between attempts |

The probe sends `Cache-Control: no-store`, does not follow redirects, and gives each attempt
five seconds. By default it retries responses other than `200`, so a listener that answers before
its upstream routes are loaded is not treated as ready. List other accepted codes in `status` for
a custom probe, and give a longer `timeout` for a proxy that is slow to load a large configuration.

Readiness applies to every target, including `proxy:` targets Mecone does not manage.

`settle:` is a flat pause after the probe succeeds, for a proxy that reports ready slightly
before it is. Keep it small; it is added to every run.

## Interpolation

Commands, environment entries, file contents, file paths, readiness paths and a `proxy:` URL are
expanded before use. A name Mecone does not know is **left exactly as written**, so an nginx
`${host}` or a shell `${HOME}` passes through untouched.

| Variable | Value | Available in |
|---|---|---|
| `${NAME}` | The target's catalog name | Everywhere |
| `${LISTEN_HOST}` | `127.0.0.1` | Everywhere; meaningful for a target Mecone manages |
| `${LISTEN_PORT}` | The port a managed proxy must listen on | Everywhere; `0` for a `proxy:` target |
| `${LISTEN_ADDR}` | `${LISTEN_HOST}:${LISTEN_PORT}` | Everywhere |
| `${ORIGIN_HOST}` | Host the target uses to reach its origin | Everywhere |
| `${ORIGIN_PORT}` | Port of the target's origin | Everywhere |
| `${ORIGIN_URL}` | `http://${ORIGIN_HOST}:${ORIGIN_PORT}` | Everywhere |
| `${WORKDIR}` | Where the target's files are rendered: its `workdir`, or a temporary directory | Managed targets; empty for a `proxy:` target |
| `${CONFIG_FILE}` | Path the target reads the **first** `files:` entry from | Managed targets that declare `files:` |
| `${PID}` | The process Mecone started | A foreground `process:` `stop` command only; rejected with `background: true` |

`${LISTEN_PORT}` is a free port on `127.0.0.1`, found immediately before the target starts,
except on a named docker network where it is `container_port`. A proxy that binds a port of its
own choosing instead of this one will never be reached.

`${CONFIG_FILE}` is resolved before any file is written, so a file's *content* can refer to it —
useful for a proxy whose main configuration names a second file.

## Files

A `files:` entry renders one configuration file for the target, with `${...}` expanded, into a
temporary directory created for that run and deleted when the target stops.

| Field | Required | Meaning |
|---|---|---|
| `source` | one of the two | Path to a template file, relative to the configuration file |
| `content` | one of the two | The file's text, written inline |
| `target` | yes | Where the target reads the file: an **absolute** path for `docker:`, a path **relative** to the working directory for `process:` |

For a `docker:` target the rendered file is staged on this machine and bind-mounted at `target`
read-only (`--volume <staged>:<target>:ro`). For a `process:` target it is written at that path
inside `${WORKDIR}`, which is the directory the command runs in, so a relative `target:` resolves
the way the command reads it.

Setting `workdir:` therefore moves the rendered files with it, and files written into a directory
you chose stay there afterwards; only the temporary directory Mecone creates is deleted.

## Examples

### A proxy that is already running

```yaml
run:
  protocols: [h1]
  timeout: 30s

origin:
  # the origin must be reachable from wherever the proxy runs
  listen: 0.0.0.0
  advertise: mecone-host.example.com

targets:
  - name: staging-edge
    proxy:
      url: https://edge.staging.example.com
      insecure: true
    readiness:
      status: [200]
      timeout: 15s
```

### A proxy in a container

```yaml
targets:
  - name: nginx
    docker:
      image: nginx
      tag: "1.30.4-alpine"
      pull: missing
      files:
        - source: conf/nginx.conf
          target: /etc/nginx/nginx.conf
    settle: 250ms
```

`conf/nginx.conf`, beside the configuration file, is a normal nginx configuration with two
substitutions. nginx's own `${...}` variables are left alone:

```nginx
server {
    listen ${LISTEN_PORT};
    location / {
        proxy_pass ${ORIGIN_URL};
        proxy_set_header Host ${host};
    }
}
```

A target that takes its configuration on the command line names it instead:

```yaml
targets:
  - name: trickster
    docker:
      image: ghcr.io/trickstercache/trickster
      tag: "2.0.5"
      args: ["-config", "/etc/trickster/trickster.yaml"]
      files:
        - source: conf/trickster.yaml
          target: /etc/trickster/trickster.yaml
```

### A proxy as a local process

```yaml
targets:
  - name: varnish-local
    process:
      start: "varnishd -F -a ${LISTEN_ADDR} -f ${CONFIG_FILE} -n ${WORKDIR}"
      env: ["VARNISH_LOG=${WORKDIR}/varnish.log"]
      files:
        - target: default.vcl
          content: |
            vcl 4.1;
            backend default {
                .host = "${ORIGIN_HOST}";
                .port = "${ORIGIN_PORT}";
            }
    readiness:
      timeout: 20s
```

`start` runs in the foreground here, so Mecone signals it to stop. A start command that returns
once the proxy is up needs both flags:

```yaml
targets:
  - name: squid-local
    process:
      background: true
      start: "squid -f ${CONFIG_FILE} -N -z && squid -f ${CONFIG_FILE}"
      stop: "squid -f ${CONFIG_FILE} -k shutdown"
      files:
        - source: conf/squid.conf
          target: squid.conf
```

## Running a catalog

```bash
bin/mecone run -config catalog.yaml -json results.json -yaml results.yaml -csv rows.csv
```

For each target, in catalog order and up to `target_concurrency` at a time, Mecone starts an
origin on its own port, starts the proxy, waits for the readiness probe to answer through it,
runs the selected tests, and stops the proxy. Progress lines and errors go to stderr. When the
run finishes, stdout gets a table of every target's overall score, then a score table per target;
the findings behind those scores go to the `-json`, `-yaml`, `-csv` and `-md` files.

A target that never starts, never becomes ready, or fails mid-run is recorded as a failed run
with the reason, and the remaining targets still run. The command then exits non-zero, naming
the targets that could not be tested, *after* the report and any `-json`, `-yaml`, `-csv` and `-md`
files are written.

`mecone report` scores the same file later, and `-min-must` holds **every** target to the bar
separately — a target that could not be tested fails it outright:

```bash
bin/mecone report -min-must 90 -baseline previous.json results.json
```

## The industry-wide catalog

`catalogs/industry-wide/` holds a catalog of well-known open-source proxies, each pinned to a
released image and given a configuration under `catalogs/industry-wide/conf/` that forwards every
path to its own Mecone origin, with caching enabled where the proxy has it.

```bash
make industry-test
```

That writes a timestamped `report-<time>.yaml`, `report-<time>.csv` and `report-<time>.md` into
`catalogs/industry-wide/reports/<YYYY-MM-DD-hhmmss>/`, where all three are gitignored. `make trickster-test` does the
same for `catalogs/trickster-baseline/`, which tests the newest Trickster release alongside the one
running on this machine; that catalog's own comments say how to set the local instance up. The point is an honest reading of
each proxy as configured there — the results depend heavily on those configurations, and a
different configuration of the same proxy will score differently.
