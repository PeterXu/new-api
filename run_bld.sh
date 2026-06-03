DATE_PREFIX=$(date +%y%m%d)
GIT_VERSION=$(git describe --tags --always 2>/dev/null || echo 'dev')
VERSION=${DATE_PREFIX}.${GIT_VERSION}
#VERSION=$VERSION docker-compose build # depends on args in docker-compose.yml.
docker compose build --build-arg VERSION=$VERSION
