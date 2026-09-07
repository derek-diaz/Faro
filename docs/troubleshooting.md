# Investigating a broken site

Open **Devices**, select the affected device, and choose **Fix a broken site**.

1. Start a fresh capture, reproduce the problem on that device, and refresh the results. The capture reads Faro's DNS logs and includes correlated addresses belonging to the same device.
2. Review blocked domains and failed DNS responses. Historical blocking is a clue; each result also explains the current policy. Select relevant domains and choose **Allow temporarily**.
3. Retry the site. Choose **Keep exceptions** if the test helped, or **Undo test** to remove the temporary exceptions.

Tests last ten minutes and apply to **every device using the selected protection**. They allow exact domains, up to twenty per test. Subdomains must be selected separately. Existing permanent exceptions and other tests remain unchanged when a test is undone. Keeping a test adds permanent exceptions to the protection in which that test started and replaces conflicting custom blocks for those domains.

Closing the browser does not end a test. Faro stores its expiry and checks for policy changes every fifteen seconds, then reloads DNS. Resolver reloads and DNS or application caches can delay the visible effect. If a reload fails, Faro restores the previous configuration and retries temporal changes. Expired tests can still be explicitly kept or removed; they may be cleaned up when a later test starts.

Temporary tests require standalone Faro. A disconnected replica cannot guarantee expiry of a controller's rendered exception, so deployments using redundancy can inspect captures but cannot start these tests. Finish or undo active tests before starting replica pairing.

Portable backups exclude temporary tests. A failed backup restore rolls back to the prior local test state along with the rest of the database.

# Responsiveness

The activity table loads independently of counts and the timeline. Visible first-page rows refresh every five seconds; summaries refresh every thirty seconds. Refreshes do not overlap, and changing filters or closing an inspector cancels obsolete requests. Totals can briefly lag incoming rows.

Dashboard data renders as each necessary request completes. DNS health probes and reverse-name discovery run separately, with bounded concurrency and timeouts. Cached names appear on subsequent inventory refreshes. Domain inspectors skip the broader related-event search because they display their own domain-specific request history.

For a repeatable performance comparison using 200,000 synthetic requests, run:

```sh
go test ./internal/api/handlers -run '^$' -bench BenchmarkHistoryReads -benchtime=3x -count=1
```

These benchmarks measure local handler/database work. They do not include browser rendering, deployment hardware, or network latency.
