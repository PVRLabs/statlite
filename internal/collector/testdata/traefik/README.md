# Traefik 3.8.0 recorded scrapes

These are unmodified Prometheus responses from the Traefik service spike. The
fixture ran one Traefik instance with the file provider, two Python HTTP
backends, and a dedicated metrics listener. It enabled service, router and
entrypoint labels with default request header label behavior. Each
`*-before` scrape precedes its named batch. The service-change captures retain
the same beta router while renaming its service from `beta@file` to
`beta-new@file`. The restart captures were taken after a proxy restart.
