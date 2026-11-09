// Passkey sign-in and registration. The server sends WebAuthn options as JSON
// with base64url-encoded binary fields; the browser API needs ArrayBuffers.

function fromBase64url(value) {
  const base64 = value.replace(/-/g, "+").replace(/_/g, "/");
  const padded = base64 + "=".repeat((4 - (base64.length % 4)) % 4);
  const binary = atob(padded);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i);
  }
  return bytes.buffer;
}

function toBase64url(buffer) {
  const bytes = new Uint8Array(buffer);
  let binary = "";
  for (const byte of bytes) {
    binary += String.fromCharCode(byte);
  }
  return btoa(binary)
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/, "");
}

function decodeDescriptors(descriptors) {
  return (descriptors || []).map((descriptor) => ({
    ...descriptor,
    id: fromBase64url(descriptor.id),
  }));
}

function creationOptions(options) {
  const publicKey = options.publicKey;
  return {
    ...publicKey,
    challenge: fromBase64url(publicKey.challenge),
    user: { ...publicKey.user, id: fromBase64url(publicKey.user.id) },
    excludeCredentials: decodeDescriptors(publicKey.excludeCredentials),
  };
}

function requestOptions(options) {
  const publicKey = options.publicKey;
  return {
    ...publicKey,
    challenge: fromBase64url(publicKey.challenge),
    allowCredentials: decodeDescriptors(publicKey.allowCredentials),
  };
}

function attestationJSON(credential) {
  const response = credential.response;
  return {
    id: credential.id,
    rawId: toBase64url(credential.rawId),
    type: credential.type,
    authenticatorAttachment: credential.authenticatorAttachment || undefined,
    clientExtensionResults: credential.getClientExtensionResults(),
    response: {
      clientDataJSON: toBase64url(response.clientDataJSON),
      attestationObject: toBase64url(response.attestationObject),
      transports: response.getTransports ? response.getTransports() : [],
    },
  };
}

function assertionJSON(credential) {
  const response = credential.response;
  return {
    id: credential.id,
    rawId: toBase64url(credential.rawId),
    type: credential.type,
    authenticatorAttachment: credential.authenticatorAttachment || undefined,
    clientExtensionResults: credential.getClientExtensionResults(),
    response: {
      clientDataJSON: toBase64url(response.clientDataJSON),
      authenticatorData: toBase64url(response.authenticatorData),
      signature: toBase64url(response.signature),
      userHandle: response.userHandle
        ? toBase64url(response.userHandle)
        : undefined,
    },
  };
}

async function postJSON(url, csrfToken, body) {
  const response = await fetch(url, {
    method: "POST",
    credentials: "same-origin",
    headers: {
      "Content-Type": "application/json",
      "X-CSRFToken": csrfToken,
    },
    body: JSON.stringify(body || {}),
  });
  let data = {};
  try {
    data = await response.json();
  } catch {
    // A non-JSON error page; fall through to the generic message.
  }
  if (!response.ok) {
    throw new Error(data.error || "The request failed. Please try again.");
  }
  return data;
}

function showError(button, error) {
  const container = button.closest("section, main") || document;
  const output = container.querySelector("[data-passkey-error]");
  if (!output) {
    return;
  }
  // The user closing the browser dialog is not an error worth showing.
  if (error && error.name === "NotAllowedError") {
    output.classList.add("d-hide");
    return;
  }
  output.textContent =
    error instanceof Error ? error.message : "Passkey request failed.";
  output.classList.remove("d-hide");
}

async function signIn(button) {
  const options = await postJSON(
    button.dataset.beginUrl,
    button.dataset.csrfToken,
  );
  const credential = await navigator.credentials.get({
    publicKey: requestOptions(options),
  });
  let url = button.dataset.finishUrl;
  if (button.dataset.next) {
    url += "?" + new URLSearchParams({ next: button.dataset.next });
  }
  const result = await postJSON(
    url,
    button.dataset.csrfToken,
    assertionJSON(credential),
  );
  window.location.assign(result.redirect);
}

async function register(button) {
  const container = button.closest("section") || document;
  const nameInput = container.querySelector("[data-passkey-name]");
  const options = await postJSON(
    button.dataset.beginUrl,
    button.dataset.csrfToken,
  );
  const credential = await navigator.credentials.create({
    publicKey: creationOptions(options),
  });
  const result = await postJSON(
    button.dataset.finishUrl,
    button.dataset.csrfToken,
    {
      name: nameInput ? nameInput.value : "",
      credential: attestationJSON(credential),
    },
  );
  window.location.assign(result.redirect);
}

function supported() {
  return (
    typeof window.PublicKeyCredential === "function" &&
    typeof navigator.credentials?.create === "function"
  );
}

function initPasskeys() {
  const available = supported();
  document
    .querySelectorAll(
      "[data-passkey-login], [data-passkey-register], [data-passkey-register-form]",
    )
    .forEach((element) => element.classList.toggle("d-hide", !available));
  document
    .querySelectorAll("[data-passkey-unsupported]")
    .forEach((element) => element.classList.toggle("d-hide", available));
}

document.addEventListener("click", async (event) => {
  const button = event.target.closest(
    "[data-passkey-login], [data-passkey-register]",
  );
  if (!button || button.disabled) {
    return;
  }
  event.preventDefault();
  button.disabled = true;
  try {
    if (button.hasAttribute("data-passkey-login")) {
      await signIn(button);
    } else {
      await register(button);
    }
  } catch (error) {
    showError(button, error);
  } finally {
    button.disabled = false;
  }
});

document.addEventListener("turbo:load", initPasskeys);
document.addEventListener("DOMContentLoaded", initPasskeys);
