# Build the recorder configuration SPA and refresh the embedded
# bundle that internal/web/web.go ships in the recorder binary.
#
# Runs the same Vite build a developer would run from web/ — uses
# the NODE_IMAGE pinned in the top-level Makefile so the build is
# reproducible on any host with Docker. The container mounts both
# web/ (the source) and internal/web/dist/ (the embed target) and
# runs npm ci + npm run build, then mirrors the resulting dist
# into the embed directory.
#
# Run this whenever you change anything under web/. Commit the
# resulting internal/web/dist/ diff alongside your web/ source diff
# so go build stays self-contained.

WEB_RUN = docker run --rm -it \
	--network=host \
	-v $(PWD):/s -w /s/web \
	$(NODE_IMAGE)

web:
	$(eval export DOCKER_BUILDKIT=1)
	$(WEB_RUN) sh -c '\
		npm ci --no-audit --no-fund && \
		npm run build && \
		rm -rf ../internal/web/dist && \
		cp -r dist ../internal/web/dist'
	@echo ""
	@echo "Embedded SPA refreshed. internal/web/dist/ is the new bundle."
	@echo "Commit the internal/web/dist/ diff alongside your web/ source diff."

# Linter+typecheck pass. Runs the same tsc -b that vite build runs
# but stops at the type check, so CI can fail fast on type errors
# without paying the bundle cost.
web-typecheck:
	$(WEB_RUN) sh -c 'npm ci --no-audit --no-fund && npm run typecheck'
