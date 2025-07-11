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
	docker tag wukongim registry.cn-shanghai.aliyuncs.com/wukongim/wukongim:v2.2.1-20250624-dev
	docker push registry.cn-shanghai.aliyuncs.com/wukongim/wukongim:v2.2.1-20250624-dev
deploy-v2:
	docker buildx build -t wukongim . --platform linux/amd64,linux/arm64
	docker tag wukongim registry.cn-shanghai.aliyuncs.com/wukongim/wukongim:v2.2.0-20250426
	docker tag wukongim wukongim/wukongim:v2.2.0-20250426
	docker tag wukongim ghcr.io/wukongim/wukongim:v2.2.0-20250426
	docker tag wukongim ghcr.io/wukongim/wukongim:v2
	docker push registry.cn-shanghai.aliyuncs.com/wukongim/wukongim:v2.2.0-20250426
	docker push wukongim/wukongim:v2.2.0-20250426
	docker push ghcr.io/wukongim/wukongim:v2
deploy-latest-v2:
	docker build -t wukongim .
	docker tag wukongim registry.cn-shanghai.aliyuncs.com/wukongim/wukongim:v2
	docker tag wukongim wukongim/wukongim:v2
	docker push registry.cn-shanghai.aliyuncs.com/wukongim/wukongim:v2
	docker push wukongim/wukongim:v2		

# docker push registry.cn-shanghai.aliyuncs.com/wukongim/wukongim:v1.2
# docker push registry.cn-shanghai.aliyuncs.com/wukongim/wukongim:v1.2-dev
