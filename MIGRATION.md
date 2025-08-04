# Migration Notes

## vNext

- Go module requires Go 1.23.
- Replaced custom `github.com/binwiederhier/discordgo` fork with upstream `github.com/bwmarrin/discordgo`.
  Thread helper functions were renamed; any external integrations using the old fork should adjust accordingly.
- Updated all dependencies to their latest stable releases.
