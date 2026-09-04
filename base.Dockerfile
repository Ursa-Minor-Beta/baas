# Base image for BaaS. Build it before the runtime image:
#
#   docker build -f base.Dockerfile -t baas-base:local .
#
# Stage 1 compiles the aipandoc fork of Pandoc; stage 2 assembles the runtime
# on top of a Selenium standalone-chrome image.
#
# Note: `selenium/standalone-chrome` may not publish arm64 manifests for all tags.
# If you see "no match for platform in manifest", build with `--platform=linux/amd64`
# or override `SELENIUM_BASE_IMAGE` to an arm64-capable base.
ARG SELENIUM_BASE_IMAGE="selenium/standalone-chrome:143.0-chromedriver-143.0-20251212"
FROM haskell:9.10.2 AS pandoc-build-stage

ARG AIPANDOC_COMMIT="3ac23e0"

RUN apt update && apt install -y \
        build-essential \
        ca-certificates \
        libgmp-dev \
        liblua5.4-dev \
        pkg-config \
        zlib1g-dev \
        unzip

WORKDIR /projects
RUN curl https://codeload.github.com/aantich/aipandoc/zip/${AIPANDOC_COMMIT} -o /projects/aipandoc.zip && \
    unzip /projects/aipandoc.zip -d /projects && \
    mv /projects/aipandoc-${AIPANDOC_COMMIT} /projects/aipandoc && \
    rm -rf /projects/aipandoc.zip

WORKDIR /projects/aipandoc
RUN stack build && stack install

# Runtime stage: Production base image with all pre-built dependencies
# Updated to December 2025 build for security patches
FROM ${SELENIUM_BASE_IMAGE}

ARG NODE_VERSION=22.13.0

# OCI-compliant image metadata
LABEL org.opencontainers.image.title="BaaS base" \
      org.opencontainers.image.description="Base image for BaaS: Chrome, Pandoc, Node.js and document-processing dependencies" \
      org.opencontainers.image.source="https://github.com/Ursa-Minor-Beta/baas" \
      org.opencontainers.image.licenses="Apache-2.0" \
      pandoc.commit="${AIPANDOC_COMMIT}" \
      node.version="${NODE_VERSION}"

# Create non-root user and install all dependencies in single layer
USER root
RUN groupadd -r -g 65532 appgroup && \
    useradd -r -u 65532 -g appgroup -d /app -s /sbin/nologin -c "Application User" appuser && \
    # Apply security updates first (fixes CVE-2025-68973, CVE-2025-38666, etc.)
    apt-get update && \
    DEBIAN_FRONTEND=noninteractive apt-get upgrade -y && \
    # Install runtime dependencies
    apt-get install -y --no-install-recommends \
        ca-certificates curl dumb-init jq \
        libreoffice poppler-utils antiword qpdf mupdf-tools wmctrl graphicsmagick ghostscript \
        python3 python3-pip xvfb fluxbox \
        build-essential pkg-config libcairo2-dev libpango1.0-dev \
        libjpeg-dev libgif-dev librsvg2-dev libpixman-1-dev && \
    apt-get purge -y apt-utils lsb-release software-properties-common && \
    # Install Node.js LTS
    curl -fsSL "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-x64.tar.gz" -o node.tar.gz && \
    tar -xzf node.tar.gz -C /usr/local --strip-components=1 && \
    rm node.tar.gz && \
    # Fix CVE-2025-64756: Upgrade npm to version with fixed glob package
    npm install -g npm@latest && \
    # Install Python packages (use system pip to install globally for all Python versions)
    # Includes security-critical packages: urllib3>=2.6.3 (CVE-2025-66418, CVE-2025-66471, CVE-2026-21441)
    # and pdfminer.six>=20251230 (GHSA-f83h-ghpp-7wcc)
    rm -rf /opt/venv && \
    /usr/bin/python3 -m pip install --no-cache-dir --break-system-packages \
        pymupdf openparse openpyxl Pillow python-docx csvkit \
        'urllib3>=2.6.3' 'pdfminer.six>=20251230' && \
    # Also install in selenium venv if it exists
    if [ -f /home/seluser/venv/bin/pip ]; then \
        /home/seluser/venv/bin/pip install --no-cache-dir \
            pymupdf openparse openpyxl Pillow python-docx csvkit \
            'urllib3>=2.6.3' 'pdfminer.six>=20251230'; \
    fi && \
    # Create complete directory structure
    mkdir -p /app/tmp /app/run /app/scripts /app/profile /app/.cache /app/.local && \
    mkdir -p /app/.config/chrome/policies/managed /app/.fluxbox && \
    # Create symlink for backward compatibility with /scripts path
    ln -sf /app/scripts /scripts && \
    echo "session.screen0.workspaces: 1" > /app/.fluxbox/init && \
    echo "session.doubleClickInterval: 250" >> /app/.fluxbox/init && \
    echo '{"DisableExtensions": true, "DisablePlugins": true, "BlockThirdPartyCookies": true, "SafeBrowsingEnabled": true}' > \
        /app/.config/chrome/policies/managed/policies.json && \
    # Set ownership and permissions in one go
    chown -R appuser:appgroup /app && \
    chmod 750 /app && \
    chmod 770 /app/tmp /app/run && \
    chmod 750 /app/scripts /app/profile /app/.cache /app/.local && \
    chmod 440 /app/.config/chrome/policies/managed/policies.json && \
    apt-get autoremove -y && apt-get clean && \
    rm -rf /var/lib/apt/lists/* /var/cache/apt/* /var/log/* /tmp/* /var/tmp/* \
           /usr/bin/apt* /usr/bin/dpkg* /root/.bash_history \
           /usr/share/doc/* /usr/share/man/* /usr/share/info/*

# Copy pre-built Pandoc binary
COPY --from=pandoc-build-stage --chown=appuser:appgroup /root/.local/bin/pandoc /usr/bin/pandoc

WORKDIR /app

# Configure runtime environment with security hardening
ENV HOME=/app \
    XDG_CONFIG_HOME=/app/.config \
    XDG_CACHE_HOME=/app/.cache \
    XDG_DATA_HOME=/app/.local \
    XDG_RUNTIME_DIR=/app/tmp \
    TMPDIR=/app/tmp \
    CHROME_EXTENSIONS_DIR=/app/profile/Extensions \
    PLAYWRIGHT_BROWSERS_PATH=0 \
    SCRIPTS_DIR=/app/scripts \
    NODE_ENV=production \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PYTHONHASHSEED=random \
    PYTHONPATH=/usr/local/lib/python3.12/dist-packages:/usr/lib/python3/dist-packages \
    VIRTUAL_ENV="" \
    VIRTUAL_ENV_DISABLE_PROMPT=1 \
    DEBIAN_FRONTEND=noninteractive \
    LANG=C.UTF-8

# Switch to non-root user for CIS compliance
USER appuser
