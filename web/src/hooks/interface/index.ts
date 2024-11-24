export namespace Table {
  export interface Pageable {
    pageNum: number;
    pageSize: number;
    total: number;
  }
  export interface TableStateProps {
    tableData: any[];
    pageable: Pageable;
    searchParam: {
      [key: string]: any;
    };
    searchInitParam: {
      [key: string]: any;
    };
    totalParam: {
      [key: string]: any;
    };
    icon?: {
      [key: string]: any;
    };
  }
}

// eslint-disable-next-line @typescript-eslint/no-namespace
export namespace HandleData {
  export type MessageType = '' | 'success' | 'warning' | 'info' | 'error';
}

// eslint-disable-next-line @typescript-eslint/no-namespace
export namespace Theme {
  export type ThemeType = 'light' | 'inverted' | 'dark';
  export type GreyOrWeakType = 'grey' | 'weak';
}
