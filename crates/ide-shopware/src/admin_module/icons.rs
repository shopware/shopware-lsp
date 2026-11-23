//! The icons of the Administration: the Meteor icon kit the project has
//! installed. Its files live below `node_modules`, outside the scanner, so
//! the two directories are listed here and the list is kept for a while.

use std::path::Path;
use std::sync::{Arc, Mutex, PoisonError};
use std::time::{Duration, Instant};

use swlsp_uriutil::filepath;

/// Where the Administration is in a project, below its root.
const ADMINISTRATIONS: [&[&str]; 3] = [
    &["src", "Administration"],
    &["vendor", "shopware", "administration"],
    &["vendor", "shopware", "platform", "src", "Administration"],
];
/// The icons of the kit, below the Administration.
const ICONS: [&str; 7] = [
    "Resources",
    "app",
    "administration",
    "node_modules",
    "@shopware-ag",
    "meteor-icon-kit",
    "icons",
];
/// The styles of the kit: an icon is named `<style>-<file>`.
const STYLES: [&str; 2] = ["regular", "solid"];
/// How long a listing is kept.
const RECHECK_INTERVAL: Duration = Duration::from_secs(5);

#[derive(Default)]
struct Listing {
    read_at: Option<Instant>,
    names: Arc<[String]>,
}

pub struct AdminIcons {
    project_root: String,
    listing: Mutex<Listing>,
}

impl AdminIcons {
    pub fn new(project_root: impl Into<String>) -> Self {
        Self {
            project_root: project_root.into(),
            listing: Mutex::default(),
        }
    }

    /// The names of the icons, sorted; empty when the kit is not installed.
    pub fn names(&self) -> Arc<[String]> {
        let mut listing = self.listing.lock().unwrap_or_else(PoisonError::into_inner);
        if listing
            .read_at
            .is_none_or(|read_at| read_at.elapsed() >= RECHECK_INTERVAL)
        {
            listing.read_at = Some(Instant::now());
            listing.names = self.read().into();
        }
        listing.names.clone()
    }

    fn read(&self) -> Vec<String> {
        let kit = ADMINISTRATIONS.iter().find_map(|administration| {
            let mut parts = vec![self.project_root.as_str()];
            parts.extend(*administration);
            parts.extend(ICONS);
            let path = filepath::join(&parts);
            Path::new(&path).is_dir().then_some(path)
        });
        let Some(kit) = kit else {
            return Vec::new();
        };
        let mut names = Vec::new();
        for style in STYLES {
            let directory = filepath::join(&[kit.as_str(), style]);
            let Ok(entries) = std::fs::read_dir(&directory) else {
                continue;
            };
            for entry in entries.flatten() {
                let file = entry.file_name();
                let Some(icon) = file.to_str().and_then(|name| name.strip_suffix(".svg")) else {
                    continue;
                };
                names.push(format!("{style}-{icon}"));
            }
        }
        names.sort();
        names
    }
}
