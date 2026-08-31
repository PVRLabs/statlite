# Quarkus fixture contract

The accepted source families are the following. All values use the base units
shown; exposition timestamps, buckets, exemplars, and unrelated labels are
discarded before normalization.

The fixture does not manufacture a Quarkus identity marker. The standard
Micrometer exposition contains generic families, and neither those families nor
the `/q/metrics` path are reliable framework evidence. Untyped inspection does
not identify Quarkus. Users select typed inspection with `--type quarkus`.

| Concept | Family | Required labels | Unit / aggregation |
| --- | --- | --- | --- |
| HTTP requests | `http_server_requests_seconds_count` | `method`, `outcome`, `status` | count; sum mutually exclusive status series |
| HTTP duration | `http_server_requests_seconds_sum` | `method`, `outcome`, `status` | seconds; sum matching timer series |
| 404 | request count family | `status=404` | count; zero is valid when label exists |
| 4xx / 5xx | request count family | `status` | count by status class |
| CPU | `process_cpu_usage` | none | ratio [0,1]; gauge |
| heap used | `jvm_memory_used_bytes` | `area=heap` | bytes; sum pools |
| process identity | `process_start_time_seconds` | none | Unix seconds; gauge |
| uptime (optional) | `process_uptime_seconds` | none | seconds; gauge |

Required compatibility is at least one finite recognized runtime family among
`process_cpu_usage`, `jvm_memory_used_bytes`, or
`process_start_time_seconds`. HTTP request meters are lazy and are therefore an
optional capability: an idle application with no
`http_server_requests_seconds_count` series remains compatible. Duration,
status classes, CPU, heap, and process identity are optional capabilities. A
syntactically valid unrelated scrape is not compatible. The `uri`, `route`, `exception`, `outcome`, pool, and other
labels are never persisted. Histogram `_bucket` families and duplicate
representations are ignored, and a missing `status` label does not produce a
zero status count.

The committed `.prom` files are reviewable contract examples. `capture.sh`
creates and validates fresh captures from the pinned application, using
`Accept: text/plain`; generated captures are not committed. Its traffic checks
prove the optional HTTP capability when exercised.
