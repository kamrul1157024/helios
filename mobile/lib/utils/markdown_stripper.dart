/// Strip markdown formatting from text for natural TTS output.
String stripMarkdown(String input) {
  var text = input;

  // Remove fenced code blocks entirely (code sounds terrible spoken)
  text = text.replaceAll(RegExp(r'```[\s\S]*?```'), '');

  // Remove inline code backticks but keep the text
  text = text.replaceAllMapped(RegExp(r'`([^`]+)`'), (m) => m[1]!);

  // Remove markdown links [text](url) → keep "text"
  text = text.replaceAllMapped(RegExp(r'\[([^\]]+)\]\([^)]+\)'), (m) => m[1]!);

  // Remove heading markers
  text = text.replaceAll(RegExp(r'^#{1,6}\s+', multiLine: true), '');

  // Remove bold/italic markers
  text = text.replaceAllMapped(RegExp(r'\*{1,3}([^*]+)\*{1,3}'), (m) => m[1]!);
  text = text.replaceAllMapped(RegExp(r'_{1,3}([^_]+)_{1,3}'), (m) => m[1]!);

  // Remove bullet point markers at line start
  text = text.replaceAll(RegExp(r'^[\s]*[-*+]\s+', multiLine: true), '');

  // Remove numbered list markers
  text = text.replaceAll(RegExp(r'^[\s]*\d+\.\s+', multiLine: true), '');

  // Remove horizontal rules
  text = text.replaceAll(RegExp(r'^---+$', multiLine: true), '');

  // Collapse multiple newlines into a single one
  text = text.replaceAll(RegExp(r'\n{3,}'), '\n\n');

  return text.trim();
}
