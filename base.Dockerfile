# Base image for BaaS. Build it before the runtime image:
#
#   docker build -f base.Dockerfile -t baas-base:local .
#
# Stage 1 compiles the aipandoc fork of Pandoc; stage 2 assembles the runtime
# on top of a Selenium browser image.
#
# The default base is `standalone-chromium`, which publishes both linux/amd64
# and linux/arm64, so this builds on Apple Silicon as well as on x86. Selenium's
# `standalone-chrome` images are amd64-only, because Google ships no arm64
# Chrome for Linux. Overriding SELENIUM_BASE_IMAGE with one of those on an
# arm64 host fails with "no match for platform in manifest"; add
# --platform=linux/amd64 if you need it anyway.
ARG SELENIUM_BASE_IMAGE="selenium/standalone-chromium:143.0"
ARG AIPANDOC_COMMIT="3ac23e0829d7426a4026216dff2f78721095e04c"

# Debian bookworm, not the bullseye-based default tag: bullseye-security has
# passed EOL and now serves an expired Release file, which makes apt-get update
# exit non-zero and fails the build.
#
# This image ships GHC 9.10.3 with system-ghc:true / install-ghc:false, while
# aipandoc pins resolver lts-24.9, which wants exactly 9.10.2. The build steps
# below pass --no-system-ghc --install-ghc so Stack fetches the GHC the
# resolver asks for instead of failing on the mismatch.
FROM haskell:9.10.3-bookworm AS pandoc-build-stage

ARG AIPANDOC_COMMIT

RUN apt-get update && apt-get install -y --no-install-recommends \
        build-essential \
        ca-certificates \
        libgmp-dev \
        liblua5.4-dev \
        pkg-config \
        zlib1g-dev \
        unzip && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /projects
RUN curl -fsSL https://codeload.github.com/aantich/aipandoc/zip/${AIPANDOC_COMMIT} -o /projects/aipandoc.zip && \
    unzip /projects/aipandoc.zip -d /projects && \
    mv /projects/aipandoc-${AIPANDOC_COMMIT} /projects/aipandoc && \
    rm -rf /projects/aipandoc.zip

WORKDIR /projects/aipandoc
RUN stack --no-system-ghc --install-ghc build && \
    stack --no-system-ghc --install-ghc install

# Runtime stage: base image with all pre-built dependencies
FROM ${SELENIUM_BASE_IMAGE}

ARG AIPANDOC_COMMIT
ARG NODE_VERSION=22.23.2
# Pinned deliberately: `npm@latest` raises its Node floor over time and silently
# breaks this build when it outruns NODE_VERSION.
ARG NPM_VERSION=11.19.1

# OCI-compliant image metadata
LABEL org.opencontainers.image.title="BaaS base" \
      org.opencontainers.image.description="Base image for BaaS: Chrome, Pandoc, Node.js and document-processing dependencies" \
      org.opencontainers.image.source="https://github.com/Ursa-Minor-Beta/baas" \
      org.opencontainers.image.licenses="Apache-2.0" \
      pandoc.commit="${AIPANDOC_COMMIT}" \
      node.version="${NODE_VERSION}" \
      npm.version="${NPM_VERSION}"

# Create non-root user and install all dependencies in single layer
USER root
RUN groupadd -r -g 65532 appgroup && \
    useradd -r -u 65532 -g appgroup -d /app -s /sbin/nologin -c "Application User" appuser && \
    # Apply security updates first (fixes CVE-2025-68973, CVE-2025-38666, etc.)
    apt-get update && \
    DEBIAN_FRONTEND=noninteractive apt-get upgrade -y && \
    # Install runtime dependencies. No compiler or -dev headers here: they were
    # only ever needed to build the canvas npm package from source, nothing in
    # the dependency tree pulls canvas any more, and every remaining native
    # package ships a prebuilt binary. Keeping them also breaks the build, since
    # this image carries Debian runtime libraries while its apt sources serve
    # Ubuntu, so the matching -dev packages are uninstallable.
    apt-get install -y --no-install-recommends \
        ca-certificates curl dumb-init jq \
        libreoffice poppler-utils antiword qpdf mupdf-tools wmctrl graphicsmagick ghostscript \
        python3 python3-pip xvfb fluxbox && \
    apt-get purge -y apt-utils lsb-release software-properties-common && \
    # Install Node.js LTS. Node names the arm64 tarball arm64 and the x86_64
    # one x64, so map from dpkg's architecture rather than hardcoding either.
    case "$(dpkg --print-architecture)" in \
        amd64) NODE_ARCH=x64 ;; \
        arm64) NODE_ARCH=arm64 ;; \
        *) echo "unsupported architecture $(dpkg --print-architecture)" >&2; exit 1 ;; \
    esac && \
    curl -fsSL "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-${NODE_ARCH}.tar.gz" -o node.tar.gz && \
    tar -xzf node.tar.gz -C /usr/local --strip-components=1 && \
    rm node.tar.gz && \
    npm install -g "npm@${NPM_VERSION}" && \
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

# The browser binary is named chromium on the chromium images and
# google-chrome on the chrome ones. Resolve it once here and expose it under a
# stable name, so nothing downstream has to know which base was used.
RUN set -eu; \
    for candidate in google-chrome google-chrome-stable chromium chromium-browser; do \
        if resolved=$(command -v "$candidate" 2>/dev/null); then break; fi; \
    done; \
    if [ -z "${resolved:-}" ]; then echo "no chrome/chromium binary in base image" >&2; exit 1; fi; \
    [ "$resolved" = /usr/bin/google-chrome ] || ln -sf "$resolved" /usr/bin/google-chrome; \
    echo "browser resolved to $resolved"

ENV BROWSER_EXECUTABLE=/usr/bin/google-chrome

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
