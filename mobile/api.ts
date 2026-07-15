import { API_BASE_URL } from "./config";

export type RuleCitation = { number: string; section: string };

export type ChatResponse = {
  answer: string;
  language: string;
  cards: string[];
  rules: RuleCitation[];
};

// askChat sends a question to the Go server's POST /chat and returns the
// grounded answer with its citations. Throws with a readable message on failure.
export async function askChat(
  question: string,
  signal?: AbortSignal
): Promise<ChatResponse> {
  let res: Response;
  try {
    res = await fetch(`${API_BASE_URL}/chat`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ question }),
      signal,
    });
  } catch {
    throw new Error(
      `Impossible de joindre le serveur (${API_BASE_URL}). Vérifie qu'il tourne et que le téléphone est sur le même Wi-Fi.`
    );
  }

  if (!res.ok) {
    let msg = `Erreur serveur (${res.status}).`;
    try {
      const body = await res.json();
      if (body?.error) msg = body.error;
    } catch {
      // non-JSON error body; keep the status message
    }
    throw new Error(msg);
  }

  return (await res.json()) as ChatResponse;
}

export type RuleText = { number: string; section: string; body: string };

// getRule fetches the full text of a rule by number, to expand a tapped citation.
export async function getRule(number: string): Promise<RuleText> {
  let res: Response;
  try {
    res = await fetch(`${API_BASE_URL}/rules/${encodeURIComponent(number)}`);
  } catch {
    throw new Error("Impossible de joindre le serveur.");
  }
  if (!res.ok) {
    throw new Error(res.status === 404 ? "Règle introuvable." : `Erreur serveur (${res.status}).`);
  }
  return (await res.json()) as RuleText;
}

// searchCards returns card-name suggestions for autocomplete. Best-effort: any
// failure yields an empty list rather than surfacing an error in the input.
export async function searchCards(q: string): Promise<string[]> {
  try {
    const res = await fetch(`${API_BASE_URL}/cards/search?q=${encodeURIComponent(q)}`);
    if (!res.ok) return [];
    return (await res.json()) as string[];
  } catch {
    return [];
  }
}

export type CardRuling = { published_at: string; source: string; comment: string };
export type CardText = {
  name: string;
  mana_cost: string;
  type_line: string;
  oracle_text: string;
  rulings: CardRuling[];
};

// getCard fetches a card's oracle text and rulings, to expand a tapped card chip.
export async function getCard(name: string): Promise<CardText> {
  let res: Response;
  try {
    res = await fetch(`${API_BASE_URL}/card?name=${encodeURIComponent(name)}`);
  } catch {
    throw new Error("Impossible de joindre le serveur.");
  }
  if (!res.ok) {
    throw new Error(res.status === 404 ? "Carte introuvable." : `Erreur serveur (${res.status}).`);
  }
  return (await res.json()) as CardText;
}

export type StreamHandlers = {
  onMeta?: (meta: { language: string; cards: string[]; rules: RuleCitation[] }) => void;
  onDelta?: (text: string) => void;
  onDone?: () => void;
  onError?: (message: string) => void;
};

// askChatStream calls POST /chat/stream and dispatches the newline-delimited
// JSON events as they arrive. React Native's fetch can't read a response body
// incrementally, so we use XMLHttpRequest (whose onprogress fires with the
// accumulated text) and parse whole lines out of it. Returns an abort function.
export function askChatStream(question: string, handlers: StreamHandlers): () => void {
  const xhr = new XMLHttpRequest();
  xhr.open("POST", `${API_BASE_URL}/chat/stream`);
  xhr.setRequestHeader("Content-Type", "application/json");

  let consumed = 0; // how far into responseText we've already parsed

  const dispatch = (line: string) => {
    let msg: any;
    try {
      msg = JSON.parse(line);
    } catch {
      return; // ignore anything that isn't a complete JSON line
    }
    switch (msg.type) {
      case "meta":
        handlers.onMeta?.({
          language: msg.language,
          cards: msg.cards ?? [],
          rules: msg.rules ?? [],
        });
        break;
      case "delta":
        handlers.onDelta?.(msg.text ?? "");
        break;
      case "done":
        handlers.onDone?.();
        break;
      case "error":
        handlers.onError?.(msg.error ?? "Erreur inconnue.");
        break;
    }
  };

  // Parse every complete (newline-terminated) line we haven't seen yet.
  const drain = () => {
    const buf = xhr.responseText;
    let nl: number;
    while ((nl = buf.indexOf("\n", consumed)) !== -1) {
      const line = buf.slice(consumed, nl).trim();
      consumed = nl + 1;
      if (line) dispatch(line);
    }
  };

  xhr.onprogress = drain;
  xhr.onload = () => {
    drain();
    // An error before streaming began comes back as a normal JSON error body
    // with a 4xx/5xx status (not NDJSON).
    if (xhr.status >= 400) {
      try {
        handlers.onError?.(JSON.parse(xhr.responseText).error ?? `Erreur ${xhr.status}.`);
      } catch {
        handlers.onError?.(`Erreur serveur (${xhr.status}).`);
      }
    }
  };
  xhr.onerror = () =>
    handlers.onError?.(
      `Impossible de joindre le serveur (${API_BASE_URL}). Vérifie qu'il tourne et le Wi-Fi.`
    );

  xhr.send(JSON.stringify({ question }));
  return () => xhr.abort();
}
