build:
	docker build -t wukongim .
push:
	docker tag wukongim wukongim/wukongim:latest
	docker push wukongim/wukongim:latest
deploy:
	docker build -t wukongim .
	docker tag wukongim wukongim/wukongim:latest
	docker push wukongim/wukongim:latest