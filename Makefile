.PHONY: install install-core install-desktop install-headless clean adopt zplug

# Default install (everything)
install: zplug install-core install-desktop

# Headless install (core only)
install-headless: zplug install-core

install-core:
	stow -t $(HOME) -d base core

install-desktop:
	stow -t $(HOME) -d base desktop

zplug:
	@[ -d $(HOME)/.zplug ] || git clone https://github.com/zplug/zplug $(HOME)/.zplug

clean:
	stow -D -t $(HOME) -d base core 2>/dev/null || true
	stow -D -t $(HOME) -d base desktop 2>/dev/null || true

adopt:
	stow --adopt -t $(HOME) -d base core
	stow --adopt -t $(HOME) -d base desktop
