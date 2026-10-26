# Hardened Containerized Ntfy

[![CI](https://github.com/underhax/ntfy-docker/actions/workflows/ci.yml/badge.svg)](https://github.com/underhax/ntfy-docker/actions/workflows/ci.yml)
[![GitHub last commit](https://img.shields.io/github/last-commit/underhax/ntfy-docker)](https://github.com/underhax/ntfy-docker/commits/main)
[![GitHub issues](https://img.shields.io/github/issues/underhax/ntfy-docker)](https://github.com/underhax/ntfy-docker/issues)
[![GitHub repo size](https://img.shields.io/github/repo-size/underhax/ntfy-docker)](https://github.com/underhax/ntfy-docker)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

Hardened, zero-trust containerized deployment architecture for [Ntfy](https://ntfy.sh/) running on a minimal scratch container with strict host Discretionary Access Control (DAC) boundaries, in-memory secret mediation, and an embedded zero-dependency healthcheck probe.

## Architecture & Security Model

* **Attack Surface Minimization:** The runtime environment contains only the static application binary, CA trust store, and entrypoint tooling. The absence of shells, package managers, and core utilities prevents post-exploitation persistence, script execution, and tool staging in the event of arbitrary code execution.
* **In-Memory Secret Mediation:** Sensitive configuration parameters (such as public URLs and access tokens) are ingested directly from mounted secret files into process memory at initialization. This prevents credential exposure in container inspection metadata, process environment dumps, and deployment manifests.
* **Host DAC & Boundary Isolation:** The service runs under a dedicated, unprivileged system account with disabled interactive login. Binding container execution to unique host identifiers enforces strict Discretionary Access Control (DAC) boundaries, preventing lateral movement and file system tampering across adjacent services.
* **Kernel & Container Hardening:** The container root filesystem is mounted strictly read-only, all Linux capabilities are dropped, privilege escalation mechanisms are disabled (`no-new-privileges`), and IPC namespace sharing is restricted.
* **Payload Execution Mitigation:** Persistent data directories holding databases and uploaded attachments are mounted with non-executable flags (`noexec`, `nosuid`, `nodev`). Any file uploaded through the service cannot be executed directly from disk.
* **Perimeter Gateway Isolation:** The service exposes no host network ports. All ingress traffic is mediated and authenticated through an upstream Edge Angie reverse proxy over an isolated container network.
* **Self-Contained Health Telemetry:** Liveness checks are performed by an internal, statically compiled probe binary querying the HTTP health endpoint. This eliminates reliance on external network clients (such as `curl` or `wget`) while maintaining continuous container supervision.

---

<details>
<summary><strong>Directory Structure</strong></summary>

Production directory layout on the host (`/home/ntfy`):

```text
├── docker-compose.yaml                # Production deployment manifest
├── .env                               # Host infrastructure & server configuration
├── secrets/                           # Docker secrets directory
│   └── ntfy_base_url                  # Ntfy public base URL (e.g. https://ntfy.example.com)
└── data/                              # SQLite databases & attachment storage
    ├── cache.db                       # SQLite message cache database
    ├── auth.db                        # SQLite authentication database (users, tokens, access)
    └── attachments/                   # Cached attachment files directory
```

</details>

---

<details>
<summary><strong>Deployment &amp; Setup</strong></summary>

### Prerequisites

* **Docker Engine & Compose:** Ensure Docker Engine and the Docker Compose plugin are installed on the host. Follow the official installation guide for [Ubuntu](https://docs.docker.com/engine/install/ubuntu/#install-using-the-repository).
* **External Gateway Network:** The stack communicates with the host's Edge reverse proxy over a shared Docker network named `frontend_gateway` (declared as `external: true`). This network is provisioned by the **[angie-docker-compose](https://github.com/webstudiobond/angie-docker-compose)** stack. If deploying standalone, create it manually:

  ```bash
  docker network create --subnet=172.20.0.0/16 frontend_gateway
  ```

### 1. Create a Dedicated System User

Choose a dedicated, unprivileged numeric ID unique to this service on your host and verify that it is not already in use by another user or group:

```bash
# Choose an unused numeric UID/GID unique to this service on your host
APP_UID=<CHOSEN_UID>

getent passwd ${APP_UID}
getent group ${APP_UID}
```

> **Important:** Both commands must return empty output. Each containerized service must have its own dedicated unprivileged user and group to enforce strict host DAC privilege separation between different stacks.

Create an unprivileged system account with a matching user-group (`-U`) and disabled login shell:

```bash
sudo useradd -m -d /home/ntfy -s /usr/sbin/nologin -u ${APP_UID} -U ntfy
```

### 2. Create Directory Layout & Assign Ownership

Create the base directory and assign ownership:

```bash
sudo mkdir -p /home/ntfy
sudo chown root:ntfy /home/ntfy
sudo chmod 0750 /home/ntfy
```

Create runtime subdirectories from `root` and assign strict DAC permissions:

```bash
sudo mkdir -p /home/ntfy/{data,secrets}

sudo chown root:ntfy /home/ntfy/secrets
sudo chmod 0750 /home/ntfy/secrets

sudo chown ntfy:ntfy /home/ntfy/data
sudo chmod 0700 /home/ntfy/data
```

### 3. Deploy Project Files

Download the production manifests directly into `/home/ntfy`:

```bash
REPO="https://raw.githubusercontent.com/underhax/ntfy-docker/main"

sudo curl -fsSL ${REPO}/docker-compose.yaml -o /home/ntfy/docker-compose.yaml
sudo curl -fsSL ${REPO}/.env.example -o /home/ntfy/.env
sudo chmod 0600 /home/ntfy/{docker-compose.yaml,.env}
sudo chown root:root /home/ntfy/{docker-compose.yaml,.env}
```

### 4. Configure Server Settings

Edit `/home/ntfy/.env` using a text editor (such as `nano`):

```bash
sudo nano /home/ntfy/.env
```

Adjust the server parameters:
* `APP_UID` and `APP_GID` matching the numeric UID/GID of your dedicated host `ntfy` account.
* `NTFY_BEHIND_PROXY='true'` to extract real client IPs from proxy headers.
* `NTFY_WEB_ROOT`: keep as `'disable'` for headless / Zero-Trust API deployments, or comment out if enabling the Web UI under an unguessable subpath via Docker Secrets.
* `NTFY_UPSTREAM_BASE_URL`: uncomment and set to `'https://ntfy.sh'` if using the official iOS app (required for Apple APNs push relay; not needed for Android or desktop).
* SQLite cache, auth paths, PRAGMA queries, and attachment size limits.

### 5. Configure Secrets

#### Base URL Setup (Production Requirement)
The public server URL is sensitive and mediated in-memory via Docker Secrets to avoid leaking in `.env`, shell history, or container inspection metadata. Create the secret file:

```bash
sudo nano /home/ntfy/secrets/ntfy_base_url
```

Enter your full public URL (e.g. `https://ntfy.example.com`), save, and lock down file permissions:

```bash
sudo chown root:ntfy /home/ntfy/secrets/*
sudo chmod 0440 /home/ntfy/secrets/*
```

#### Web UI Secret Subpath Setup (Optional)
If enabling the Web UI, hosting it under an unguessable subpath rather than predictable paths prevents automated discovery, reconnaissance, and unauthorized access attempts. Because this path acts as a secret access route, mediate it via Docker Secrets instead of exposing it in plaintext in `.env`:

1. Generate an unguessable, URL-safe alphanumeric subpath identifier (24 lowercase chars without symbols) within an ephemeral subshell:

   ```bash
   (printf 'ui_%s' "$(pwgen -s -A 24 1)" | sudo tee /home/ntfy/secrets/ntfy_web_root > /dev/null)
   sudo chown root:ntfy /home/ntfy/secrets/ntfy_web_root
   sudo chmod 0440 /home/ntfy/secrets/ntfy_web_root
   ```

2. In `docker-compose.yaml`, uncomment `ntfy_web_root` under `services.ntfy.secrets` and top-level `secrets:`.

3. In `.env`, comment out `NTFY_WEB_ROOT='disable'`.

At startup, the `init` binary injects `NTFY_WEB_ROOT` directly into process memory, exposing the Web UI strictly at `https://ntfy.example.com/<GENERATED_SUBPATH>`.

#### Web Push Setup (Optional)
If using the Web UI and requiring background browser notifications when tabs are closed (RFC 8030 Web Push), a VAPID keypair and contact email address are required. To prevent exposing cryptographic keys and administrator email (PII) in `.env`, shell history, or container metadata, mediate all Web Push credentials via Docker Secrets:

1. Generate the VAPID keypair inside an ephemeral subshell to prevent cryptographic keys from lingering in interactive shell memory or history:

   ```bash
   (VAPID=$(sudo docker compose -f /home/ntfy/docker-compose.yaml run --rm ntfy webpush keys) && \
     printf '%s\n' "${VAPID}" | awk '/web-push-public-key:/{print $2}' | sudo tee /home/ntfy/secrets/ntfy_web_push_public_key > /dev/null && \
     printf '%s\n' "${VAPID}" | awk '/web-push-private-key:/{print $2}' | sudo tee /home/ntfy/secrets/ntfy_web_push_private_key > /dev/null)
   ```

2. Create the administrative contact email using an interactive editor to avoid leaking PII into shell history (`~/.bash_history`) or process listings:

   ```bash
   sudo nano /home/ntfy/secrets/ntfy_web_push_email_address
   ```

3. Restrict ownership and permissions on the provisioned Web Push secrets:

   ```bash
   sudo chown root:ntfy /home/ntfy/secrets/ntfy_web_push_*
   sudo chmod 0440 /home/ntfy/secrets/ntfy_web_push_*
   ```

4. In `docker-compose.yaml`, uncomment `ntfy_web_push_public_key`, `ntfy_web_push_private_key`, and `ntfy_web_push_email_address` under `services.ntfy.secrets` and top-level `secrets:`.

5. In `.env`, uncomment `NTFY_WEB_PUSH_FILE='/data/webpush.db'` (and optional startup PRAGMA queries).

At container initialization, the `init` binary injects `NTFY_WEB_PUSH_PUBLIC_KEY`, `NTFY_WEB_PUSH_PRIVATE_KEY`, and `NTFY_WEB_PUSH_EMAIL_ADDRESS` directly into process memory, enabling RFC 8030 Web Push while keeping cryptographic keys and administrative contact data fully shielded from host inspection.

#### Universal Secret Mapping
Any sensitive Ntfy configuration parameter ([official documentation](https://docs.ntfy.sh/config/)) can be mapped securely through Docker Secrets without exposing credentials in `.env` or `docker inspect`:
1. Save the secret with the prefix `ntfy_` in `/home/ntfy/secrets/` (e.g. `ntfy_upstream_access_token`, `ntfy_smtp_sender_pass`, `ntfy_stripe_secret_key`, etc.).
2. Declare the secret name under `secrets` in `docker-compose.yaml` (uncomment pre-configured stubs such as `ntfy_web_root` or `ntfy_web_push_*`).
3. At container startup, the `init` binary automatically scans `/run/secrets/ntfy_*`, translates filenames into uppercase `NTFY_*` environment variables in process memory, and execs `ntfy serve`. Empty secret files are safely skipped. Plaintext secrets never appear in container inspection metadata or process environment dumps.

### 6. Host Permissions &amp; Privilege Separation

Keep deployment inputs and service configuration outside the service account's write access. The container runs with numeric `APP_UID:APP_GID` and needs write access only to `/home/ntfy/data`. A compromised process cannot replace its Compose manifest, configuration, or secret files.

| Host path | Owner | Mode | Reason |
| --- | --- | --- | --- |
| `/home/ntfy` | `root:ntfy` | `0750` | Base directory; allows service traversal without access for other users |
| `docker-compose.yaml`, `.env` | `root:root` | `0600` | Host-only manifests and environment settings |
| `secrets/` directory | `root:ntfy` | `0750` | Traverse-only access for the service group |
| `secrets/*` | `root:ntfy` | `0440` | Read-only to the service group |
| `data/` and subdirectories | `ntfy:ntfy` | `0700` | Private runtime storage and nested directories |
| `data/*` files | `ntfy:ntfy` | `0600` | Private read-write storage for SQLite databases and attachments |

#### Apply or restore host permissions

Run as a host administrator after creating files or after any configuration update:

```bash
sudo chown root:ntfy /home/ntfy
sudo chmod 0750 /home/ntfy

sudo chown root:root /home/ntfy/docker-compose.yaml /home/ntfy/.env
sudo chmod 0600 /home/ntfy/docker-compose.yaml /home/ntfy/.env

sudo chown -R root:ntfy /home/ntfy/secrets
sudo find /home/ntfy/secrets -type d -exec chmod 0750 {} +
sudo find /home/ntfy/secrets -type f -exec chmod 0440 {} +

sudo chown -R ntfy:ntfy /home/ntfy/data
sudo find /home/ntfy/data -type d -exec chmod 0700 {} +
sudo find /home/ntfy/data -type f -exec chmod 0600 {} +
```

### 7. Configure Edge Reverse Proxy (Angie)

All ingress traffic is terminated by the host's Edge reverse proxy using pre-hardened virtual host templates from **[angie-docker-compose](https://github.com/webstudiobond/angie-docker-compose)** (`data/conf.d/domains/`). Select the template that matches your operational and security requirements:

#### Choosing a Virtual Host Template

* **Standard Template ([`ntfy.conf`](https://github.com/webstudiobond/angie-docker-compose/blob/main/data/conf.d/domains/ntfy.conf)):**
  * **Use Case:** Full-featured deployments supporting the Web UI (`app.html`), browser-based topic subscriptions, file attachments, and interactive user access.
  * **Stack Requirements:** To expose the Web UI, provision an unguessable subpath secret via `secrets/ntfy_web_root` (see Section 5) and uncomment `ntfy_web_root` in `docker-compose.yaml`. For background browser push notifications when tabs are closed, provision the Web Push secrets (`ntfy_web_push_*`), uncomment them in `docker-compose.yaml`, and uncomment `NTFY_WEB_PUSH_FILE` in `.env`.
  * **Security Profile:** Implements baseline perimeter hardening while proxying dynamic paths: blocks hidden files (`.env`, `.git`) with 404, restricts HTTP methods to `GET`, `HEAD`, `POST`, `PUT`, `OPTIONS` (returns 405 for all others), and intercepts path traversal (`../`) and code injection patterns in `$request_uri` (returns 403).
* **Zero-Trust Whitelist Template ([`ntfy-whitelist.conf`](https://github.com/webstudiobond/angie-docker-compose/blob/main/data/conf.d/domains/ntfy-whitelist.conf)):**
  * **Use Case:** Headless notification gateways, infrastructure telemetry, automated alerting (Prometheus, Alertmanager, backup runners), and machine-to-machine integrations.
  * **Security Profile:** Enforces strict **Default Deny (404)**. Completely blocks the Web UI, topic enumeration, and scanner reconnaissance. Only explicitly declared topic regex patterns (e.g. `^/(ping[^/]*|monit[^/]*|telemetry[^/]*)(/.*)?$`), optional file attachment downloads (`^/file/[^/]+$`), and push gateways (Matrix) are passed upstream. Any request outside the allowlist map is rejected at the Angie perimeter with a 404 before reaching the Ntfy runtime.

#### Deployment & Placeholder Customization

1. Download your selected template to the Angie virtual hosts directory on the host:

   ```bash
   REPO="https://raw.githubusercontent.com/webstudiobond/angie-docker-compose/main"

   # For Zero-Trust Whitelist (Recommended for alerts & headless):
   sudo -u angie curl -fsSL ${REPO}/data/conf.d/domains/ntfy-whitelist.conf \
     -o /home/angie/data/conf.d/domains/ntfy.conf

   # Or for Standard with Web UI:
   # sudo -u angie curl -fsSL ${REPO}/data/conf.d/domains/ntfy.conf \
   #   -o /home/angie/data/conf.d/domains/ntfy.conf
   ```

2. Replace the template placeholder domain (`ntfy.example`) with your actual production FQDN:

   ```bash
   DOMAIN="ntfy.yourdomain.com"

   sudo -u angie sed -i "s|ntfy\.example|${DOMAIN}|g" /home/angie/data/conf.d/domains/ntfy.conf
   ```

   > **Note:** If deploying multiple Ntfy instances behind the same Angie gateway, globally substitute the `ntfy` identifier (e.g. `ntfy_prod`) to ensure unique ACME client names, upstream pools (`ntfy_backend`), and log variables across the global Angie `http` context.

3. If using the Whitelist template:
   * Adjust the `$ntfy_allowed` regex map in `ntfy.conf` to match your intended topic naming conventions.
   * If notifications include file attachments (e.g. log dumps, screenshots, or metrics reports), uncomment the attachment download endpoint (`~*^/file/[^/]+$ 1;`). Uploads are posted directly into the authorized topic endpoint, but client downloads are served from `/file/<attachment-id>.<ext>`.

4. Validate Angie configuration syntax and trigger a graceful reload using your stack's standard workflow (e.g. `angie -t` and `angie -s reload`, adapted to your container or host execution method):

   ```bash
   # Standard Angie commands (execute via your stack's CLI wrapper, docker exec, or host binary):
   angie -t && angie -s reload
   ```

### 8. Start the Stack

Pull the latest release and start the service in detached mode:

```bash
sudo docker compose -f /home/ntfy/docker-compose.yaml pull
sudo docker compose -f /home/ntfy/docker-compose.yaml up -d
```

</details>

---

<details>
<summary><strong>User &amp; Access Management</strong></summary>

Since `ntfy` is configured with `NTFY_AUTH_DEFAULT_ACCESS="deny-all"`, topics are private by default. Manage users, permissions, and access tokens directly via the CLI without entering a shell.

> **Security Note:** Never use common, predictable administrative usernames such as `admin`, `root`, or `administrator`. Using an unpredictable, dedicated administrative identifier mitigates automated account enumeration, brute-force, and credential-stuffing attacks.

### 1. Add Administrator or Standard User

Create a dedicated administrator account:

```bash
sudo docker compose -f /home/ntfy/docker-compose.yaml exec ntfy ntfy user add --role=admin <ADMIN_USERNAME>
```

Create a standard user account:

```bash
sudo docker compose -f /home/ntfy/docker-compose.yaml exec ntfy ntfy user add <USERNAME>
```

List all registered users and roles:

```bash
sudo docker compose -f /home/ntfy/docker-compose.yaml exec ntfy ntfy user list
```

### 2. Grant Topic Access

Grant read-write access to a specific topic:

```bash
sudo docker compose -f /home/ntfy/docker-compose.yaml exec ntfy ntfy access <USERNAME> "alerts" rw
```

Grant read-only access:

```bash
sudo docker compose -f /home/ntfy/docker-compose.yaml exec ntfy ntfy access <USERNAME> "alerts" ro
```

List all granted topic permissions:

```bash
sudo docker compose -f /home/ntfy/docker-compose.yaml exec ntfy ntfy access
```

### 3. Generate Access Tokens

Generate an access token for API publishing and automation:

```bash
sudo docker compose -f /home/ntfy/docker-compose.yaml exec ntfy ntfy token add <ADMIN_USERNAME>
```

List existing tokens:

```bash
sudo docker compose -f /home/ntfy/docker-compose.yaml exec ntfy ntfy token list
```

</details>

---

<details>
<summary><strong>Operation &amp; Maintenance</strong></summary>

### Verifying Service Health

Check container status and resource consumption:

```bash
sudo docker compose -f /home/ntfy/docker-compose.yaml ps
sudo docker stats --no-stream ntfy
```

### Inspecting Runtime Logs

Follow service logs in real time:

```bash
sudo docker compose -f /home/ntfy/docker-compose.yaml logs -f
```

### Updating the Image

Update to the latest release and recreate the container:

```bash
sudo docker compose -f /home/ntfy/docker-compose.yaml pull
sudo docker compose -f /home/ntfy/docker-compose.yaml up -d
```

### Tearing Down the Stack

Stop and remove the container:

```bash
sudo docker compose -f /home/ntfy/docker-compose.yaml down
```

</details>

---

<details>
<summary><strong>Development</strong></summary>

For local binary compilation, unit testing, static analysis, and CI/CD pipeline details, see **[DEVELOPMENT.md](DEVELOPMENT.md)**.

</details>

---

<details>
<summary><strong>License &amp; Attribution</strong></summary>

## License & Attribution

This repository and deployment architecture are licensed under the [MIT License](LICENSE). This project is an independent containerized deployment architecture and is not affiliated with, endorsed, or sponsored by Philipp Heckel.

**Ntfy License & Ownership:** Ntfy is open-source software dual-licensed under the [Apache License 2.0 and GNU General Public License v2.0](https://github.com/binwiederhier/ntfy/blob/main/LICENSE) and developed by [Philipp Heckel (binwiederhier)](https://github.com/binwiederhier).

</details>
