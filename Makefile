build:
	docker build -t wukongim .
push:
	docker tag wukongim wukongim/wukongim:latest-dev
	docker push wukongim/wukongim:latest-dev
deploy:
	docker build -t wukongim .
	docker tag wukongim wukongim/wukongim:latest
	docker push wukongim/wukongim:latest	
deploy-dev:
	docker build -t wukongim .
	docker tag wukongim wukongim/wukongim:latest-dev
	docker push wukongim/wukongim:latest-dev
deploy-arm:
	docker build -t wukongimarm64 . -f Dockerfile.arm64 --platform linux/arm64
	docker tag wukongimarm64 wukongim/wukongim:latest-arm64
	docker push wukongim/wukongim:latest-arm64
deploy-v2-dev:
	docker build -t wukongim .  --platform linux/amd64
	docker tag wukongim registry.cn-shanghai.aliyuncs.com/wukongim/wukongim:v2.2.4-dev
	docker push registry.cn-shanghai.aliyuncs.com/wukongim/wukongim:v2.2.4-dev
deploy-v2:
	docker buildx build -t wukongim . --platform linux/amd64,linux/arm64
	docker tag wukongim registry.cn-shanghai.aliyuncs.com/wukongim/wukongim:v2.2.1-20250624
	docker tag wukongim wukongim/wukongim:v2.2.1-20250624
	docker tag wukongim ghcr.io/wukongim/wukongim:v2.2.1-20250624
	docker tag wukongim ghcr.io/wukongim/wukongim:v2
	docker push registry.cn-shanghai.aliyuncs.com/wukongim/wukongim:v2.2.1-20250624
	docker push wukongim/wukongim:v2.2.1-20250624
	docker push ghcr.io/wukongim/wukongim:v2
deploy-latest-v2:
	docker build -t wukongim .
	docker tag wukongim registry.cn-shanghai.aliyuncs.com/wukongim/wukongim:v2
	docker tag wukongim wukongim/wukongim:v2
	docker push registry.cn-shanghai.aliyuncs.com/wukongim/wukongim:v2
	docker push wukongim/wukongim:v2		

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
