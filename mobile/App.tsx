import { useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  FlatList,
  Keyboard,
  Modal,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import {
  SafeAreaProvider,
  useSafeAreaInsets,
} from "react-native-safe-area-context";
import { StatusBar } from "expo-status-bar";

import {
  askChatStream,
  getCard,
  getRule,
  searchCards,
  CardText,
  RuleCitation,
  RuleText,
} from "./api";

type Message = {
  id: string;
  role: "user" | "assistant";
  text: string;
  rules?: RuleCitation[];
  cards?: string[];
  error?: boolean;
};

let nextId = 0;
const newId = () => String(nextId++);

// applySuggestion replaces the in-progress card name at the end of the input
// with the chosen full name. It finds the longest run of trailing words that is
// a prefix of the suggestion (so "Doubling Sea" → "Doubling Season", not
// "Doubling Doubling Season"), falling back to replacing just the last word.
function applySuggestion(input: string, suggestion: string): string {
  const words = input.split(/\s+/).filter(Boolean);
  const lower = suggestion.toLowerCase();
  let k = 1;
  for (let n = Math.min(6, words.length); n >= 1; n--) {
    const tail = words.slice(words.length - n).join(" ").toLowerCase();
    if (lower.startsWith(tail)) {
      k = n;
      break;
    }
  }
  const kept = words.slice(0, words.length - k).join(" ");
  return (kept ? kept + " " : "") + suggestion + " ";
}

export default function App() {
  // SafeAreaProvider must wrap the tree so useSafeAreaInsets() can read the
  // status-bar / navigation-bar insets (needed because Android draws edge-to-edge).
  return (
    <SafeAreaProvider>
      <ChatScreen />
    </SafeAreaProvider>
  );
}

function ChatScreen() {
  const insets = useSafeAreaInsets();
  const [messages, setMessages] = useState<Message[]>([]);
  const [input, setInput] = useState("");
  const [loading, setLoading] = useState(false); // request in flight (disables input)
  const [pending, setPending] = useState(false); // retrieving, before the first token
  const [keyboardHeight, setKeyboardHeight] = useState(0);
  const [detail, setDetail] = useState<Detail>(null);
  const [suggestions, setSuggestions] = useState<string[]>([]);
  const listRef = useRef<FlatList<Message>>(null);
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const fragRef = useRef(""); // latest typed word, to drop stale search results

  // Autocomplete keys on the word currently being typed (≥3 chars). Results are
  // debounced; a stale response (user kept typing) is ignored.
  const onInputChange = (t: string) => {
    setInput(t);
    const frag = t.match(/(\S+)$/)?.[1] ?? "";
    fragRef.current = frag;
    if (debounceRef.current) clearTimeout(debounceRef.current);
    if (frag.length < 3) {
      setSuggestions([]);
      return;
    }
    debounceRef.current = setTimeout(() => {
      searchCards(frag).then((names) => {
        if (fragRef.current === frag) setSuggestions(names);
      });
    }, 250);
  };

  const selectSuggestion = (name: string) => {
    setInput((cur) => applySuggestion(cur, name));
    setSuggestions([]);
    fragRef.current = "";
    if (debounceRef.current) clearTimeout(debounceRef.current);
  };

  // openDetail fetches a rule or a card and shows it in the modal. The guard
  // ignores a slow response if the user has since tapped something else.
  const openDetail = <T,>(
    kind: "rule" | "card",
    key: string,
    fetcher: (k: string) => Promise<T>,
    put: (d: T) => Detail
  ) => {
    setDetail({ kind, key, loading: true });
    fetcher(key)
      .then((data) => setDetail((d) => (d?.kind === kind && d.key === key ? put(data) : d)))
      .catch((e: Error) =>
        setDetail((d) => (d?.kind === kind && d.key === key ? { kind, key, loading: false, error: e.message } : d))
      );
  };
  const openRule = (number: string) =>
    openDetail("rule", number, getRule, (rule) => ({ kind: "rule", key: number, loading: false, rule }));
  const openCard = (name: string) =>
    openDetail("card", name, getCard, (card) => ({ kind: "card", key: name, loading: false, card }));
  const closeDetail = () => setDetail(null);

  // Track the keyboard height ourselves: KeyboardAvoidingView's "padding" mode
  // leaves residual padding on Android edge-to-edge (the bar stays stuck up
  // after the keyboard closes). Driving it from the events guarantees a clean
  // return to 0 on dismiss.
  useEffect(() => {
    const showEvt = Platform.OS === "ios" ? "keyboardWillShow" : "keyboardDidShow";
    const hideEvt = Platform.OS === "ios" ? "keyboardWillHide" : "keyboardDidHide";
    const show = Keyboard.addListener(showEvt, (e) => setKeyboardHeight(e.endCoordinates.height));
    const hide = Keyboard.addListener(hideEvt, () => setKeyboardHeight(0));
    return () => {
      show.remove();
      hide.remove();
    };
  }, []);

  const send = () => {
    const question = input.trim();
    if (!question || loading) return;

    const assistantId = newId();
    setMessages((m) => [
      ...m,
      { id: newId(), role: "user", text: question },
      { id: assistantId, role: "assistant", text: "" }, // filled by the stream
    ]);
    setInput("");
    setSuggestions([]);
    setLoading(true);
    setPending(true);

    const patch = (fn: (msg: Message) => Message) =>
      setMessages((m) => m.map((x) => (x.id === assistantId ? fn(x) : x)));

    askChatStream(question, {
      onMeta: (meta) => patch((x) => ({ ...x, rules: meta.rules, cards: meta.cards })),
      onDelta: (text) => {
        setPending(false);
        patch((x) => ({ ...x, text: x.text + text }));
      },
      onDone: () => {
        setLoading(false);
        setPending(false);
      },
      onError: (msg) => {
        patch((x) => ({ ...x, text: msg, error: true }));
        setLoading(false);
        setPending(false);
      },
    });
  };

  return (
    <View style={styles.root}>
      <StatusBar style="light" />

      {/* Header background extends up behind the status bar via top inset padding. */}
      <View style={[styles.header, { paddingTop: insets.top + 12 }]}>
        <Text style={styles.headerTitle}>Assistant règles MTG</Text>
      </View>

      <View style={[styles.flex, { paddingBottom: keyboardHeight }]}>
        <FlatList
          ref={listRef}
          style={styles.flex}
          contentContainerStyle={styles.listContent}
          data={messages}
          keyExtractor={(m) => m.id}
          renderItem={({ item }) =>
            // Hide the assistant placeholder until the first token arrives.
            item.role === "assistant" && item.text === "" && !item.error ? null : (
              <Bubble message={item} onRulePress={openRule} onCardPress={openCard} />
            )
          }
          keyboardShouldPersistTaps="handled"
          onContentSizeChange={() => listRef.current?.scrollToEnd({ animated: true })}
          ListEmptyComponent={
            <Text style={styles.empty}>
              Pose une question de règles (stack, priorité, interactions de cartes…).
              Réponses fondées sur les Comprehensive Rules, avec numéros de règle cités.
            </Text>
          }
        />

        {pending && (
          <View style={styles.thinking}>
            <ActivityIndicator />
            <Text style={styles.thinkingText}>Recherche dans les règles…</Text>
          </View>
        )}

        {suggestions.length > 0 && !loading && (
          <ScrollView
            horizontal
            keyboardShouldPersistTaps="handled"
            showsHorizontalScrollIndicator={false}
            style={styles.suggestBar}
            contentContainerStyle={styles.suggestContent}
          >
            {suggestions.map((s) => (
              <Pressable key={s} style={styles.suggestChip} onPress={() => selectSuggestion(s)}>
                <Text style={styles.suggestText}>{s}</Text>
              </Pressable>
            ))}
          </ScrollView>
        )}

        {/* Input + footer are one continuous grey block whose bottom padding
            (the safe-area inset) fills down behind the navigation bar, so there's
            no dark gap between the bar and the buttons. */}
        <View style={[styles.bottomBar, { paddingBottom: insets.bottom }]}>
          <View style={styles.inputRow}>
            <TextInput
              style={styles.input}
              value={input}
              onChangeText={onInputChange}
              placeholder="Ta question…"
              placeholderTextColor="#8a8f98"
              multiline
              editable={!loading}
            />
            <Pressable
              style={[styles.sendBtn, (loading || !input.trim()) && styles.sendBtnDisabled]}
              onPress={send}
              disabled={loading || !input.trim()}
            >
              <Text style={styles.sendBtnText}>Envoyer</Text>
            </Pressable>
          </View>
          <Text style={styles.footer}>Données de cartes : Scryfall</Text>
        </View>
      </View>

      <DetailModal detail={detail} onClose={closeDetail} />
    </View>
  );
}

type Detail = {
  kind: "rule" | "card";
  key: string; // rule number or card name — used to guard against stale responses
  loading: boolean;
  rule?: RuleText;
  card?: CardText;
  error?: string;
} | null;

function DetailModal({ detail, onClose }: { detail: Detail; onClose: () => void }) {
  return (
    <Modal visible={!!detail} transparent animationType="fade" onRequestClose={onClose}>
      {/* Tap the backdrop to close; the inner Pressable swallows taps on the card. */}
      <Pressable style={styles.modalBackdrop} onPress={onClose}>
        <Pressable style={styles.modalCard} onPress={() => {}}>
          {detail?.loading && <ActivityIndicator style={{ marginVertical: 20 }} />}
          {detail?.error && <Text style={styles.modalError}>{detail.error}</Text>}

          {detail?.rule && (
            <>
              <Text style={styles.modalNumber}>{detail.rule.number}</Text>
              <Text style={styles.modalSection}>{detail.rule.section}</Text>
              <ScrollView style={styles.modalScroll}>
                <Text style={styles.modalBody}>{detail.rule.body}</Text>
              </ScrollView>
            </>
          )}

          {detail?.card && (
            <>
              <Text style={styles.modalNumber}>{detail.card.name}</Text>
              <Text style={styles.modalSection}>
                {[detail.card.mana_cost, detail.card.type_line].filter(Boolean).join("  ·  ")}
              </Text>
              <ScrollView style={styles.modalScroll}>
                {!!detail.card.oracle_text && (
                  <Text style={styles.modalBody}>{detail.card.oracle_text}</Text>
                )}
                {detail.card.rulings.length > 0 && (
                  <>
                    <Text style={styles.modalRulingsLabel}>
                      Rulings ({detail.card.rulings.length})
                    </Text>
                    {detail.card.rulings.map((r, i) => (
                      <View key={i} style={styles.ruling}>
                        <Text style={styles.rulingDate}>{r.published_at || "—"}</Text>
                        <Text style={styles.modalBody}>{r.comment}</Text>
                      </View>
                    ))}
                  </>
                )}
              </ScrollView>
            </>
          )}

          <Pressable style={styles.modalClose} onPress={onClose}>
            <Text style={styles.modalCloseText}>Fermer</Text>
          </Pressable>
        </Pressable>
      </Pressable>
    </Modal>
  );
}

function Bubble({
  message,
  onRulePress,
  onCardPress,
}: {
  message: Message;
  onRulePress: (number: string) => void;
  onCardPress: (name: string) => void;
}) {
  const isUser = message.role === "user";
  return (
    <View style={[styles.row, isUser ? styles.rowUser : styles.rowAssistant]}>
      <View
        style={[
          styles.bubble,
          isUser ? styles.bubbleUser : styles.bubbleAssistant,
          message.error && styles.bubbleError,
        ]}
      >
        <Text style={[styles.bubbleText, isUser && styles.bubbleTextUser]}>
          {message.text}
        </Text>

        {/* Citations rendered distinctly from the answer body. */}
        {!!message.rules?.length && (
          <View style={styles.citations}>
            <Text style={styles.citationsLabel}>Règles citées (touchez pour lire)</Text>
            <View style={styles.chipRow}>
              {message.rules.map((r) => (
                <Pressable
                  key={r.number}
                  style={({ pressed }) => [styles.chip, styles.chipTappable, pressed && styles.chipPressed]}
                  onPress={() => onRulePress(r.number)}
                >
                  <Text style={styles.chipText}>{r.number}</Text>
                </Pressable>
              ))}
            </View>
          </View>
        )}
        {!!message.cards?.length && (
          <View style={styles.citations}>
            <Text style={styles.citationsLabel}>Cartes (touchez pour voir)</Text>
            <View style={styles.chipRow}>
              {message.cards.map((c) => (
                <Pressable
                  key={c}
                  style={({ pressed }) => [styles.chip, styles.cardChip, pressed && styles.chipPressed]}
                  onPress={() => onCardPress(c)}
                >
                  <Text style={styles.chipText}>{c}</Text>
                </Pressable>
              ))}
            </View>
          </View>
        )}
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  root: { flex: 1, backgroundColor: "#0f1115" },
  flex: { flex: 1 },
  header: {
    paddingBottom: 14,
    paddingHorizontal: 16,
    backgroundColor: "#1b1f27",
    borderBottomWidth: StyleSheet.hairlineWidth,
    borderBottomColor: "#2b303b",
  },
  headerTitle: { color: "#f2f4f8", fontSize: 18, fontWeight: "700" },
  listContent: { padding: 12, gap: 10 },
  empty: {
    color: "#8a8f98",
    textAlign: "center",
    marginTop: 48,
    paddingHorizontal: 24,
    lineHeight: 20,
  },
  row: { flexDirection: "row" },
  rowUser: { justifyContent: "flex-end" },
  rowAssistant: { justifyContent: "flex-start" },
  bubble: { maxWidth: "88%", borderRadius: 14, paddingHorizontal: 14, paddingVertical: 10 },
  bubbleUser: { backgroundColor: "#3b6fed", borderBottomRightRadius: 4 },
  bubbleAssistant: { backgroundColor: "#1b1f27", borderBottomLeftRadius: 4 },
  bubbleError: { backgroundColor: "#3a1d22", borderWidth: 1, borderColor: "#7a2e39" },
  bubbleText: { color: "#e7e9ee", fontSize: 15, lineHeight: 21 },
  bubbleTextUser: { color: "#ffffff" },
  citations: { marginTop: 10 },
  citationsLabel: {
    color: "#8a8f98",
    fontSize: 11,
    textTransform: "uppercase",
    letterSpacing: 0.5,
    marginBottom: 5,
  },
  chipRow: { flexDirection: "row", flexWrap: "wrap", gap: 6 },
  chip: {
    backgroundColor: "#242a35",
    borderRadius: 8,
    paddingHorizontal: 8,
    paddingVertical: 3,
    borderWidth: 1,
    borderColor: "#333a48",
  },
  cardChip: { backgroundColor: "#2a2438", borderColor: "#463a5e" },
  chipTappable: { borderColor: "#3b6fed" },
  chipPressed: { backgroundColor: "#2f3a52" },
  chipText: { color: "#c7ccd6", fontSize: 12, fontWeight: "600" },
  thinking: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
    paddingHorizontal: 16,
    paddingBottom: 6,
  },
  thinkingText: { color: "#8a8f98", fontSize: 13 },
  suggestBar: { maxHeight: 44, backgroundColor: "#141821" },
  suggestContent: { paddingHorizontal: 10, paddingVertical: 6, gap: 6, alignItems: "center" },
  suggestChip: {
    backgroundColor: "#242a35",
    borderRadius: 16,
    paddingHorizontal: 12,
    paddingVertical: 7,
    borderWidth: 1,
    borderColor: "#333a48",
  },
  suggestText: { color: "#c7ccd6", fontSize: 13 },
  bottomBar: {
    paddingHorizontal: 10,
    paddingTop: 10,
    borderTopWidth: StyleSheet.hairlineWidth,
    borderTopColor: "#2b303b",
    backgroundColor: "#141821",
  },
  inputRow: { flexDirection: "row", alignItems: "flex-end", gap: 8 },
  input: {
    flex: 1,
    maxHeight: 120,
    backgroundColor: "#1b1f27",
    borderRadius: 12,
    paddingHorizontal: 14,
    paddingVertical: 10,
    color: "#f2f4f8",
    fontSize: 15,
  },
  sendBtn: {
    backgroundColor: "#3b6fed",
    borderRadius: 12,
    paddingHorizontal: 16,
    paddingVertical: 12,
  },
  sendBtnDisabled: { backgroundColor: "#2b3247" },
  sendBtnText: { color: "#ffffff", fontWeight: "700" },
  footer: { color: "#5b616c", fontSize: 11, textAlign: "center", paddingTop: 8 },
  modalBackdrop: {
    flex: 1,
    backgroundColor: "rgba(0,0,0,0.6)",
    justifyContent: "center",
    padding: 20,
  },
  modalCard: {
    backgroundColor: "#1b1f27",
    borderRadius: 16,
    padding: 18,
    maxHeight: "80%",
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: "#333a48",
  },
  modalNumber: { color: "#f2f4f8", fontSize: 20, fontWeight: "700" },
  modalSection: { color: "#8a8f98", fontSize: 13, marginTop: 2, marginBottom: 12 },
  modalScroll: { marginBottom: 14 },
  modalBody: { color: "#e7e9ee", fontSize: 15, lineHeight: 22 },
  modalRulingsLabel: {
    color: "#8a8f98",
    fontSize: 12,
    textTransform: "uppercase",
    letterSpacing: 0.5,
    marginTop: 16,
    marginBottom: 8,
  },
  ruling: {
    marginBottom: 12,
    paddingLeft: 10,
    borderLeftWidth: 2,
    borderLeftColor: "#333a48",
  },
  rulingDate: { color: "#6f7684", fontSize: 12, marginBottom: 2 },
  modalError: { color: "#e79aa5", fontSize: 15, marginVertical: 16 },
  modalClose: {
    alignSelf: "flex-end",
    backgroundColor: "#2b3247",
    borderRadius: 10,
    paddingHorizontal: 18,
    paddingVertical: 10,
  },
  modalCloseText: { color: "#ffffff", fontWeight: "700" },
});
