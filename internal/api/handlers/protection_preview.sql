-- Bindings: profile ID, normalized draft rules (JSON), maximum requests.
WITH recent AS MATERIALIZED (
    SELECT q.id, julianday(q.timestamp) AS observed, lower(rtrim(q.domain, '.')) AS domain,
           q.client_ip, coalesce(q.device_id, address.device_id) AS device_id
    FROM dns_queries q
    LEFT JOIN device_addresses address ON address.address = q.client_ip
    LEFT JOIN device_protection_memberships membership
        ON membership.device_id = coalesce(q.device_id, address.device_id)
    LEFT JOIN device_protection_assignments legacy
        ON legacy.client_ip = q.client_ip AND (q.device_id IS NULL OR address.device_id = q.device_id OR address.device_id IS NULL)
    WHERE julianday(q.timestamp) BETWEEN julianday('now', '-1 day') AND julianday('now')
      AND coalesce(membership.protection_id, legacy.protection_id,
                   (SELECT id FROM protection_profiles WHERE is_default = 1)) = ?1
    ORDER BY julianday(q.timestamp) DESC, q.id DESC
    LIMIT (?3 + 1)
), sample AS MATERIALIZED (
    SELECT * FROM recent ORDER BY observed DESC, id DESC LIMIT ?3
), domains AS (
    SELECT DISTINCT domain FROM sample
), saved_lists AS (
    SELECT b.id FROM blocklists b JOIN protection_blocklists p ON p.blocklist_id = b.id
    WHERE p.protection_id = ?1 AND b.enabled = 1
), draft_lists AS (
    SELECT id FROM blocklists WHERE enabled = 1 AND id IN (SELECT value FROM json_each(?2, '$.blocklist_ids'))
), rules AS MATERIALIZED (
    SELECT d.domain,
        d.domain NOT IN (SELECT domain FROM protection_allow_entries WHERE protection_id = ?1)
        AND (d.domain IN (SELECT domain FROM protection_block_entries WHERE protection_id = ?1)
             OR EXISTS (SELECT 1 FROM blocklist_entries WHERE domain = d.domain AND blocklist_id IN saved_lists)) AS saved_block,
        d.domain NOT IN (SELECT value FROM json_each(?2, '$.allow_domains'))
        AND (d.domain IN (SELECT value FROM json_each(?2, '$.block_domains'))
             OR EXISTS (SELECT 1 FROM blocklist_entries WHERE domain = d.domain AND blocklist_id IN draft_lists)) AS draft_block
    FROM domains d
    WHERE NOT EXISTS (SELECT 1 FROM troubleshooting_exceptions t WHERE t.protection_id = ?1
                      AND t.domain = d.domain AND julianday(t.expires_at) > julianday('now'))
), matches AS MATERIALIZED (
    SELECT s.*, CASE WHEN r.saved_block AND r.draft_block THEN 'still_blocked'
                     WHEN r.draft_block THEN 'newly_blocked' ELSE 'newly_allowed' END AS outcome
    FROM sample s JOIN rules r USING (domain)
    WHERE r.saved_block OR r.draft_block
), domain_counts AS (
    SELECT outcome, domain, count(*) AS hits FROM matches GROUP BY outcome, domain
), ranked_domains AS (
    SELECT *, sum(hits) OVER (PARTITION BY outcome) AS total,
           row_number() OVER (PARTITION BY outcome ORDER BY hits DESC, domain) AS rank
    FROM domain_counts
), changed_devices AS MATERIALIZED (
    SELECT device_id, min(client_ip) AS client_ip, count(*) AS hits FROM matches
    WHERE outcome IN ('newly_blocked', 'newly_allowed')
    GROUP BY device_id, CASE WHEN device_id IS NULL THEN client_ip END
), top_devices AS (
    SELECT * FROM changed_devices ORDER BY hits DESC, client_ip, device_id LIMIT 5
)
SELECT outcome, domain AS label, '' AS address, hits, total FROM ranked_domains WHERE rank <= 5
UNION ALL
SELECT 'devices', coalesce(nullif(trim(d.name), ''), nullif(trim(n.name), ''), local.hostname, q.client_ip),
       q.client_ip, q.hits, (SELECT count(*) FROM changed_devices)
FROM top_devices q
LEFT JOIN devices d ON d.id = q.device_id
LEFT JOIN device_names n ON n.device_id = d.id AND n.source = 'unifi'
LEFT JOIN device_addresses owner ON owner.address = q.client_ip
LEFT JOIN dns_records local ON (owner.device_id IS NULL OR owner.device_id = q.device_id) AND local.id = (
    SELECT id FROM dns_records WHERE value = q.client_ip ORDER BY updated_at DESC, id DESC LIMIT 1)
UNION ALL
SELECT 'sample', '', '', min(count(*), ?3), count(*) > ?3 FROM recent
ORDER BY outcome, hits DESC, label, address
