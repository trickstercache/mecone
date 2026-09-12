# Writing Mecone tests

Mecone tests are YAML. A suite is a directory, a group is a file in it, and a test is an entry
in a group. The built-in suites live in [`suites/`](../suites) and are embedded in the binary.
Point `-suites-dir` at a directory with the same layout to run your own.

```text
suites/
  caching/
    suite.yaml        # describes the suite
    storage.yaml      # group "storage"
    freshness.yaml    # group "freshness"
```

Files decode strictly. A misspelled field is a load error, not a silently ignored key, and
every test is validated before anything runs. `mecone list -suites-dir <dir>` is the quickest
way to check your files.

## Suites and groups

`suite.yaml`:

```yaml
id: caching            # must equal the directory name
title: HTTP Caching
description: How a shared cache stores, reuses and validates responses, per RFC 9111.
```

A group file:

```yaml
group: storage         # must equal the file name, without .yaml
title: Storing responses
description: Whether the proxy keeps a response for reuse, and when it must not.
refs: [RFC9111#3]      # inherited by tests that cite no refs of their own
tests:
  - ...
```

## Tests

| Field | Required | Meaning |
|---|---|---|
| `id` | yes | Unique across every suite; lowercase kebab-case. Start it with the group name (`storage-no-store`). |
| `title` | yes | One sentence stating the behavior being checked, phrased as a fact about a correct proxy |
| `level` | yes | `must`, `should`, `may` or `info`; see [Choosing a level](#choosing-a-level) |
| `refs` | yes* | Sections the test exercises, as `RFC<number>#<section>`, such as `RFC9111#5.2.2.5`. *May be inherited from the group. |
| `protocols` | no | Client protocols the test applies to (`h1`, `h2`, `h2c`, `h3`); default is all |
| `requires` | no | IDs of tests that must pass (on the same protocol) for this one to be meaningful |
| `responses` | no | Named responses the origin can give; see below |
| `steps` | yes | The exchange, in order; at least one step must send a request |

## Responses

```yaml
responses:
  cacheable:
    status: 200                      # default 200; 1xx goes in interim
    headers:                         # "Name: value" lines; order and repeats are kept
      - "Cache-Control: max-age=3600"
      - 'ETag: "v1"'
      - "Last-Modified: ${now-1h}"
    body: hello                      # or body_size: N for N predictable bytes (0-9a-z repeating)
    ranges: true                     # serve bytes Range requests (206/416) from this body
    ignore_conditionals: true        # send 200 even when If-None-Match/If-Modified-Since match
    interim:                         # 1xx responses sent first
      - status: 103
        headers: ["Link: </style.css>; rel=preload; as=style"]
    trailers: ["Mecone-Checksum: abc123"]
    delay: 2s                        # wait before responding
    disconnect: true                 # abort the connection instead of responding
```

When a test defines one response, every request step uses it. When it defines several, each
request step names one with `respond_with`. A test with no responses gets an empty `200`.

Unless `ignore_conditionals` is set, the origin answers a matching `If-None-Match` (weak
comparison) or `If-Modified-Since` with `304`, like a real server.

### Dates

In any header value, `${now}`, `${now+DURATION}` and `${now-DURATION}` render as an HTTP-date
(IMF-fixdate), with the offset in Go duration syntax (`90s`, `1h30m`). Response headers render
when the origin builds the response; request headers render when the client sends the request.

## Steps

A step is either a pause or a request.

```yaml
steps:
  - arrange: true                     # a request that only sets up state
    respond_with: cacheable
    expect: {forwarded: true}
  - wait: 2s                          # let the stored response go stale
  - method: GET                       # default GET
    path: /asset.css                  # appended to the test's URL; optional
    query: v=1                        # optional
    headers: ['If-None-Match: "v1"']
    body: ""                          # request body; optional
    expect:
      status: 304
      forwarded: false
```

Adjacent request steps with `parallel: true` start concurrently. Use this for
requirements involving collapsed forwarding or simultaneous cache access;
expectations and origin responses still belong to each step independently.

```yaml
steps:
  - parallel: true
    headers: ["X-Mecone-Select: alpha"]
    respond_with: alpha
  - parallel: true
    headers: ["X-Mecone-Select: beta"]
    respond_with: beta
```

**`arrange`** marks a step whose only job is to set up the scenario. If its expectations fail
(or its request fails), the outcome is *inconclusive* rather than *fail*: the proxy never got
into the state the test is about.

## Expectations

| Field | Checks |
|---|---|
| `status` | The response status, as a code or a list of acceptable codes: `status: [200, 416]` |
| `headers` | Response header lines (see matching below) |
| `headers_absent` | Field names that must not be in the response |
| `body` | Exact response body |
| `interim` | 1xx statuses the client must have received, in this order (others may be interleaved) |
| `trailers` | Trailer field lines (see matching below) |
| `forwarded` | `true`: the proxy must contact the origin for this step. `false`: it must answer by itself. |
| `forwarded_headers` | Header lines the origin must have received |
| `forwarded_headers_absent` | Field names the origin must not have received |

Header and trailer lines are matched against the *combined* field value (all lines of that
name joined with `, `):

| Line | Passes when |
|---|---|
| `Via` | the field is present |
| `Cache-Control: max-age=60` | the combined value equals `max-age=60` |
| `Age: ~^[0-9]+$` | the combined value matches the regular expression after `~` |

A request that fails at the transport level (reset, timeout, malformed response) always fails
its step.

## Choosing a level

Pick the level from the specification text, not from how important the behavior feels.

- `must`: the RFC says MUST, MUST NOT, REQUIRED or SHALL, and a conforming intermediary has no
  choice.
- `should`: the RFC says SHOULD or RECOMMENDED.
- `may`: optional behavior a good implementation offers, such as reusing a fresh response.
  Caching itself is optional, so "the cache reused it" is `may`. "The cache didn't reuse what it
  mustn't" is `must`.
- `info`: behavior worth recording that the RFCs don't judge.

When a requirement only applies once an optional capability is present (for example, "a
response served from cache carries `Age`"), write the capability as its own `may` test and have
the requirement test `require` it. Proxies without the capability are then *skipped* rather
than failed.

## Style

- One behavior per test; titles are specific facts ("… is never reused", "… carries an Age
  header").
- Write tests from the RFC text. Don't copy definitions, wording or structure from other test
  suites; Mecone must remain an independent, Apache-licensed work.
- Keep responses minimal: only the headers the behavior depends on.
- Prefer `forwarded` to guessing from response headers. The origin log is authoritative.

## Checklist

1. `mecone list -suites-dir suites` loads without errors.
2. `mecone run -tests <your-id>` gives the expected outcome with no proxy.
3. Add the expected outcomes against a bare origin and a non-caching `httputil.ReverseProxy` to
   `suites/suites_test.go` when they are well defined.
