# Provena Linux amd64 deployment

1. Create a virtual environment and install the Python dependencies: `python3 -m venv .venv && .venv/bin/pip install -r requirements.txt`.
2. Copy `config.yaml.example` to `config.yaml`, fill in the AI provider values, then run `chmod 600 config.yaml`.
3. Run `bash start.sh` for an initial local test. The service listens only on `127.0.0.1:9090`.
4. For a persistent service, create a `provena` system account, install `provena.service.example` as `/etc/systemd/system/provena.service`, then run `systemctl daemon-reload && systemctl enable --now provena`.
5. Use `nginx-provena.conf.example` as the Nginx virtual-host configuration. Replace the domain and certificate paths, create the Basic Auth file with `htpasswd -c /etc/nginx/.htpasswd-provena <admin-name>`, then validate and reload Nginx with `nginx -t && systemctl reload nginx`.
6. At the cloud firewall and host firewall, allow only TCP 80 and 443. Do not expose 9090.

Runtime requirements: Linux amd64, Python 3.10+ and the packages in `requirements.txt`.

The bundled tools include Linux builds of amass, ffuf, gau, subfinder, dddd and Katana. dddd is the supported fingerprint and vulnerability scanner; the standalone Nuclei configuration is disabled.
