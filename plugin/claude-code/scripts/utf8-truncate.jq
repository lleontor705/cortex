# Input: hook object with stdout; $limit is the maximum UTF-8 bytes.
# Produces rune-safe passive-capture content and auditable byte metadata.
def prefix_within($text; $limit; $low; $high):
  if $low >= $high then $text[0:$low]
  else (($low + $high + 1) / 2 | floor) as $mid
    | if ($text[0:$mid] | utf8bytelength) <= $limit
      then prefix_within($text; $limit; $mid; $high)
      else prefix_within($text; $limit; $low; $mid - 1)
      end
  end;

(.stdout // "") as $text
| ($text | utf8bytelength) as $original
| prefix_within($text; $limit; 0; ($text | length)) as $stored
| ($stored | utf8bytelength) as $stored_bytes
| {
    content: $stored,
    truncated: ($original > $stored_bytes),
    original_bytes: $original,
    stored_bytes: $stored_bytes
  }
