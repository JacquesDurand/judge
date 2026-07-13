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
