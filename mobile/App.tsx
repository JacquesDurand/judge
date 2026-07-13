import { useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  FlatList,
  Keyboard,
  Platform,
  Pressable,
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

import { askChat, RuleCitation } from "./api";

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
  const [loading, setLoading] = useState(false);
  const [keyboardHeight, setKeyboardHeight] = useState(0);
  const listRef = useRef<FlatList<Message>>(null);

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

  const send = async () => {
    const question = input.trim();
    if (!question || loading) return;

    setMessages((m) => [...m, { id: newId(), role: "user", text: question }]);
    setInput("");
    setLoading(true);
    try {
      const res = await askChat(question);
      setMessages((m) => [
        ...m,
        {
          id: newId(),
          role: "assistant",
          text: res.answer,
          rules: res.rules,
          cards: res.cards,
        },
      ]);
    } catch (e) {
      setMessages((m) => [
        ...m,
        { id: newId(), role: "assistant", text: (e as Error).message, error: true },
      ]);
    } finally {
      setLoading(false);
    }
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
          renderItem={({ item }) => <Bubble message={item} />}
          keyboardShouldPersistTaps="handled"
          onContentSizeChange={() => listRef.current?.scrollToEnd({ animated: true })}
          ListEmptyComponent={
            <Text style={styles.empty}>
              Pose une question de règles (stack, priorité, interactions de cartes…).
              Réponses fondées sur les Comprehensive Rules, avec numéros de règle cités.
            </Text>
          }
        />

        {loading && (
          <View style={styles.thinking}>
            <ActivityIndicator />
            <Text style={styles.thinkingText}>Recherche dans les règles…</Text>
          </View>
        )}

        {/* Input + footer are one continuous grey block whose bottom padding
            (the safe-area inset) fills down behind the navigation bar, so there's
            no dark gap between the bar and the buttons. */}
        <View style={[styles.bottomBar, { paddingBottom: insets.bottom }]}>
          <View style={styles.inputRow}>
            <TextInput
              style={styles.input}
              value={input}
              onChangeText={setInput}
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
    </View>
  );
}

function Bubble({ message }: { message: Message }) {
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
            <Text style={styles.citationsLabel}>Règles citées</Text>
            <View style={styles.chipRow}>
              {message.rules.map((r) => (
                <View key={r.number} style={styles.chip}>
                  <Text style={styles.chipText}>{r.number}</Text>
                </View>
              ))}
            </View>
          </View>
        )}
        {!!message.cards?.length && (
          <View style={styles.citations}>
            <Text style={styles.citationsLabel}>Cartes</Text>
            <View style={styles.chipRow}>
              {message.cards.map((c) => (
                <View key={c} style={[styles.chip, styles.cardChip]}>
                  <Text style={styles.chipText}>{c}</Text>
                </View>
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
  chipText: { color: "#c7ccd6", fontSize: 12, fontWeight: "600" },
  thinking: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
    paddingHorizontal: 16,
    paddingBottom: 6,
  },
  thinkingText: { color: "#8a8f98", fontSize: 13 },
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
});
