import React from 'react';

export type BlockType = 'text' | 'heading' | 'button' | 'image' | 'divider' | 'spacer' | 'list' | 'social' | 'video' | 'table' | 'columns';

export interface Column {
  id: string;
  blocks: Block[];
  width?: string;
  style?: React.CSSProperties;
}

export interface Block {
  id: string;
  type: BlockType;
  content: {
    loopVariable?: string;
    loopItemVariable?: string;
    [key: string]: any;
  };
  style: React.CSSProperties;
}

export interface EmailDesign {
  blocks: Block[];
  bodyStyle: {
    backgroundColor?: string;
    fontFamily?: string;
    padding?: string;
    contentWidth?: string;
    color?: string;
  };
  preheader?: string;
}

export interface BlockDefinition {
  type: BlockType;
  label: string;
  icon: React.ReactNode;
  defaultContent: any;
  defaultStyle: React.CSSProperties;
}
