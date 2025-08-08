# Static Site Deployer

> Work In Progress

## Dev setup

Run caddy with the plugin
```bash
xcaddy run
```

Build caddy with the plugin
```bash
xcaddy build
```

Once running, update caddy config
```bash
curl localhost:2019/load -H "Content-Type: application/json" -d @tests/assets/config.json 
```

Upload a file
```bash
curl -T tests/assets/index.txt http://localhost:8888
```
