PLATFORMS ?= linux/amd64,linux/arm64
IMAGE ?= wukongim
V2_DEV_IMAGE ?= registry.cn-shanghai.aliyuncs.com/wukongim/wukongim:v2.2.6-dev
V2_VERSION ?= v2.2.1-20250624

build:
	docker buildx build --platform $(PLATFORMS) -t $(IMAGE) --output type=oci,dest=$(IMAGE).tar .
push:
	docker buildx build --platform $(PLATFORMS) -t wukongim/wukongim:latest-dev --push .
deploy:
	docker buildx build --platform $(PLATFORMS) -t wukongim/wukongim:latest --push .
deploy-dev:
	docker buildx build --platform $(PLATFORMS) -t wukongim/wukongim:latest-dev --push .
deploy-arm:
	docker build -t wukongimarm64 . -f Dockerfile.arm64 --platform linux/arm64
	docker tag wukongimarm64 wukongim/wukongim:latest-arm64
	docker push wukongim/wukongim:latest-arm64
deploy-v2-dev:
	docker buildx build --platform $(PLATFORMS) -t $(V2_DEV_IMAGE) --push .
deploy-v2:
	docker buildx build --platform $(PLATFORMS) \
		-t registry.cn-shanghai.aliyuncs.com/wukongim/wukongim:$(V2_VERSION) \
		-t wukongim/wukongim:$(V2_VERSION) \
		-t ghcr.io/wukongim/wukongim:$(V2_VERSION) \
		-t ghcr.io/wukongim/wukongim:v2 \
		--push .
deploy-latest-v2:
	docker buildx build --platform $(PLATFORMS) \
		-t registry.cn-shanghai.aliyuncs.com/wukongim/wukongim:v2 \
		-t wukongim/wukongim:v2 \
		--push .

# docker push registry.cn-shanghai.aliyuncs.com/wukongim/wukongim:v1.2
# docker push registry.cn-shanghai.aliyuncs.com/wukongim/wukongim:v1.2-dev

bench:
	go test -bench=. -benchmem -count=5 -run='^$$' -timeout 30m ./...

bench-compare:
	@if [ -z "$$(git status --porcelain)" ]; then \
		echo "Working tree is clean, proceeding..."; \
	else \
		echo "Error: working tree is dirty. Please commit or stash changes first."; \
		exit 1; \
	fi
	@CURRENT_BRANCH=$$(git rev-parse --abbrev-ref HEAD); \
	BASE_BRANCH=$${BASE_BRANCH:-main}; \
	echo "Comparing $$BASE_BRANCH vs $$CURRENT_BRANCH"; \
	git stash --include-untracked 2>/dev/null || true; \
	git checkout $$BASE_BRANCH; \
	go test -bench=. -benchmem -count=5 -run='^$$' -timeout 30m ./... > base.txt; \
	git checkout $$CURRENT_BRANCH; \
	git stash pop 2>/dev/null || true; \
	go test -bench=. -benchmem -count=5 -run='^$$' -timeout 30m ./... > pr.txt; \
	benchstat base.txt pr.txt; \
	rm -f base.txt pr.txt
