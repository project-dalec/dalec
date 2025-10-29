import { themes as prismThemes } from 'prism-react-renderer';
import type { Config } from '@docusaurus/types';
import type * as Preset from '@docusaurus/preset-classic';

const config: Config = {
  title: 'Dalec',
  tagline: 'Exterminate!',
  favicon: 'img/favicon.ico',

  // Set the production url of your site here
  url: 'https://project-dalec.github.io',
  // Set the /<baseUrl>/ pathname under which your site is served
  // For GitHub pages deployment, it is often '/<projectName>/'
  baseUrl: '/dalec/',

  // GitHub pages deployment config.
  // If you aren't using GitHub pages, you don't need these.
  organizationName: 'project-dalec', // Usually your GitHub org/user name.
  projectName: 'dalec', // Usually your repo name.

  onBrokenLinks: 'throw',

  // Even if you don't use internationalization, you can use this field to set
  // useful metadata like html lang. For example, if your site is Chinese, you
  // may want to replace "en" with "zh-Hans".
  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
  },

  presets: [
    [
      'classic',
      {
        docs: {
          routeBasePath: '/',
          sidebarPath: './sidebars.ts',
          // Please change this to your repo.
          // Remove this to remove the "edit this page" links.
          editUrl:
            'https://github.com/project-dalec/dalec/blob/main/website/',
        },
        theme: {
          customCss: './src/css/custom.css',
        },
      } satisfies Preset.Options,
    ],
  ],

  themes: ['@docusaurus/theme-mermaid'],

  markdown: {
    mermaid: true,
    hooks: {
      onBrokenMarkdownLinks: 'warn'
    },
  },

  themeConfig: {
    // Replace with your project's social card
    image: 'img/logo.png',
    navbar: {
      title: 'Dalec',
      logo: {
        alt: 'Dalec logo',
        src: 'img/logo.svg',
      },
      items: [
        {
          href: 'https://github.com/project-dalec/dalec',
          position: 'right',
          className: 'header-github-link',
          'aria-label': 'GitHub repository',
        },
      ],
    },
    footer: {
      style: 'dark',
      links: [],
      copyright: `Copyright Dalec a Series of LF Projects, LLC
      For website terms of use, trademark policy and other project policies please see lfprojects.org/policies/.`
    },
    prism: {
      theme: prismThemes.github,
      darkTheme: prismThemes.dracula,
      additionalLanguages: ['bash', 'json', 'yaml'],
    },
    colorMode: {
      defaultMode: 'dark',
      disableSwitch: false,
      respectPrefersColorScheme: true,
    },
    announcementBar: {
      id: 'announcementBar-1', // Increment on change
      content: `⭐️ If you like Dalec, please give it a star on <a target="_blank" rel="noopener noreferrer" href="https://github.com/project-dalec/dalec">GitHub</a>!</a>`,
    },
    algolia: {
      appId: 'QB3RJK7I77',
      apiKey: '2889aa0d888bd13d73eeb460f7b0f479',
      indexName: 'azureio',
    },
  } satisfies Preset.ThemeConfig,
};

export default config;
