# HTTP Tunnel Server and Client

This repository provides an HTTP tunnel server and client implementation. The server allows users to create tunnels for forwarding HTTP requests to a destination server. The client connects to the server and forwards requests to the desired destination.

---

## Features

- Create HTTP tunnels with custom or random subdomains.
- WebSocket-based communication for efficient request forwarding.
- Secure domain key validation for tunnel access.

---

## Prerequisites

- A domain name (e.g., `example.com`) and a wildcard subdomain (e.g., `*.example.com`) pointing to the server hosting this tunneling service.
- Go 1.23+ installed on your machine (if building the server manually).
- Alternatively, use the prebuilt Docker image for hosting the server.

---

## Hosting the Tunnel Server

### Option 1: Build and Run the Server Manually

1. **Clone the Repository**:
   ```bash
   git clone https://github.com/your-repo/http-tunnel.git
   cd http-tunnel
   ```

2. **Build the Server**:
   ```bash
   go build -o tunnel-server main.go
   ```

3. **Run the Server**:
   ```bash
   sudo ./tunnel-server
   ```
   The server will start listening on port `80`.

4. **Verify the Server**:
   - Open your browser and navigate to `http://<your-domain>/ping`.
   - You should see a JSON response:
     ```json
     {
       "ping": "pong",
       "tunnels": []
     }
     ```

### Option 2: Run the Server Using Docker

1. **Pull the Docker Image**:
   ```bash
   docker pull kaenova/tunnel
   ```

2. **Run the Docker Container**:
   ```bash
   docker run -d -p 80:80 kaenova/tunnel
   ```

3. **Verify the Server**:
   - Open your browser and navigate to `http://<your-domain>/ping`.
   - You should see a JSON response:
     ```json
     {
       "ping": "pong",
       "tunnels": []
     }
     ```

---

## Using the Tunnel Client

### Download the Client Binary

1. Go to the [Releases Page](https://github.com/kaenova/http-tunnels/releases). Or install by using `go install`:
  ```bash
  go install github.com/kaenova/http-tunnels@latest
  ```
2. Download the appropriate binary for your platform (e.g., Windows, macOS, Linux).

### Run the Client

```bash
http-tunnels -host http://<your-domain> <destination_server>
```
Replace `<your-tunnel-domain>` with your server's domain (e.g., `example.com`) and `<destination_server>` with the URL of the server you want to forward requests to (e.g., `http://localhost:8080`).

---

## Example Usage

### Create a Tunnel with a Custom Subdomain

1. Run the client with a custom subdomain:
   ```bash
   http-tunnels -host http://<your-domain> -subdomain mysubdomain <destination_server>
   ```

   It will run and connect to the server like this:
    ```bash
    http-tunnels -host http://localhost:80 -subdomain kaenova http://localhost:5500
    2025/05/16 16:18:09 Tunnel created with domain: fpwzd9fv_pe.localhost:80
    2025/05/16 16:18:09 Domain key: YClFmsr6BosKxaH92tV6UQ
    2025/05/16 16:18:09 Connected to tunnel server
    ```

2. Access the tunnel:
   - Open your browser and navigate to `http://fpwzd9fv_pe.localhost`.

---

## API Endpoints

### Server Endpoints

1. **Ping Endpoint**:
   - URL: `/ping`
   - Method: `GET`
   - Response:
     ```json
     {
       "ping": "pong",
       "tunnels": ["subdomain1", "subdomain2"]
     }
     ```

2. **Create New Tunnel**:
   - URL: `/new_tunnel`
   - Method: `POST`
   - Query Parameters:
     - `subdomain` (optional): Custom subdomain.
   - Response:
     ```json
     {
       "domain": "assigned-subdomain",
       "domain_key": "generated-domain-key"
     }
     ```

3. **Tunnel WebSocket**:
   - URL: `/tunnel`
   - Method: `GET`
   - Query Parameters:
     - `domain`: The domain of the tunnel.
     - `domain_key`: The domain key for validation.

---

## Notes

- Ensure your domain and wildcard subdomain (e.g., `example.com` and `*.example.com`) are properly configured to point to the server hosting this tunneling service.
- Use a reverse proxy (e.g., Nginx) if you want to run the server on a different port or behind HTTPS.

---

## License

This project is licensed under the MIT License. See the LICENSE file for details.

---

## Contributing

Feel free to submit issues or pull requests to improve the project.
